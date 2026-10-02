package chromium

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// ErrNoKeyring means the storage password could not be retrieved. Cookies
// written with the v10 prefix on Linux do not need it, so this is not always
// fatal.
var ErrNoKeyring = errors.New("chromium: no storage password available")

// Every keyring call is bounded. An ordinary call answers in milliseconds, so
// callTimeout only catches a wedged daemon. An unlock or access prompt waits
// on a person typing a password, which is what promptTimeout allows for —
// and without it, a prompt dismissed by closing its window rather than
// pressing cancel left the app waiting forever.
var (
	callTimeout   = 5 * time.Second
	promptTimeout = 2 * time.Minute
)

// storagePasswords fetches the password a browser encrypts its cookies with,
// from wherever this platform keeps it: the Keychain on macOS; the Secret
// Service on Linux, then KWallet, which is where Chromium puts it on KDE.
func storagePasswords(b Browser) ([][]byte, error) {
	if runtime.GOOS == "darwin" {
		pw, err := keychainPasswords(b.Storage)
		if err != nil {
			return nil, fmt.Errorf("%w (Keychain: %v)", ErrNoKeyring, err)
		}
		return pw, nil
	}
	secret, secretErr := secretServicePasswords(b.Keyring)
	if len(secret) > 0 {
		return secret, nil
	}
	wallet, walletErr := kwalletPasswords(b.Storage)
	if len(wallet) > 0 {
		return wallet, nil
	}
	return nil, fmt.Errorf("%w (Secret Service: %v; KWallet: %v)", ErrNoKeyring, secretErr, walletErr)
}

// sessionBus connects to the user's session bus, failing fast where there
// is none rather than letting the library guess an address.
func sessionBus() (*dbus.Conn, error) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		return nil, errors.New("no session bus")
	}
	return dbus.ConnectSessionBus()
}

// ------------------------------------------------------------ Secret Service

// The Secret Service API, which gnome-keyring, KeePassXC and Plasma 6's
// ksecretd implement. Chromium stores its password there under an
// "application" attribute naming the browser.
const (
	secretBus    = "org.freedesktop.secrets"
	secretRoot   = "/org/freedesktop/secrets"
	serviceIface = "org.freedesktop.Secret.Service"
	itemIface    = "org.freedesktop.Secret.Item"
	promptIface  = "org.freedesktop.Secret.Prompt"
)

// secretValue is the wire shape returned by Item.GetSecret.
type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// secretServicePasswords returns the password filed under the first of apps
// that has one.
func secretServicePasswords(apps []string) ([][]byte, error) {
	conn, err := sessionBus()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	svc := conn.Object(secretBus, secretRoot)

	call := func(method string, args ...any) *dbus.Call {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return svc.CallWithContext(ctx, serviceIface+"."+method, 0, args...)
	}

	// "plain" means the secret crosses the bus unencrypted. The alternative
	// is a DH handshake, which buys nothing here: the bus is the user's own
	// session, and anything that can read it can read the cookie file too.
	var output dbus.Variant
	var session dbus.ObjectPath
	if err := call("OpenSession", "plain", dbus.MakeVariant("")).Store(&output, &session); err != nil {
		return nil, fmt.Errorf("OpenSession: %v", err)
	}

	for _, app := range apps {
		var unlocked, locked []dbus.ObjectPath
		if err := call("SearchItems", map[string]string{"application": app}).
			Store(&unlocked, &locked); err != nil {
			return nil, fmt.Errorf("SearchItems: %v", err)
		}
		if len(unlocked) == 0 && len(locked) > 0 {
			opened, err := unlock(conn, svc, locked)
			if err != nil {
				return nil, err
			}
			unlocked = opened
		}
		for _, item := range unlocked {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			var s secretValue
			err := conn.Object(secretBus, item).
				CallWithContext(ctx, itemIface+".GetSecret", 0, session).Store(&s)
			cancel()
			if err == nil && len(s.Value) > 0 {
				return [][]byte{s.Value}, nil
			}
		}
	}
	return nil, fmt.Errorf("no password for %q", apps)
}

// unlock asks the Secret Service to unlock items, following the prompt the
// service may return. A locked keyring is the ordinary state after a login
// that did not unlock it, so this is a normal path rather than an error.
func unlock(conn *dbus.Conn, svc dbus.BusObject, locked []dbus.ObjectPath) ([]dbus.ObjectPath, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var opened []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := svc.CallWithContext(ctx, serviceIface+".Unlock", 0, locked).Store(&opened, &prompt); err != nil {
		return nil, fmt.Errorf("unlock: %v", err)
	}
	if prompt == "/" || prompt == "" {
		return opened, nil
	}

	// The prompt is asynchronous: it completes with a signal, so the match
	// has to be in place before it is triggered.
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(prompt),
		dbus.WithMatchInterface(promptIface),
		dbus.WithMatchMember("Completed"),
	); err != nil {
		return nil, fmt.Errorf("prompt match: %v", err)
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	if call := conn.Object(secretBus, prompt).CallWithContext(ctx, promptIface+".Prompt", 0, ""); call.Err != nil {
		return nil, fmt.Errorf("prompt: %v", call.Err)
	}
	deadline := time.After(promptTimeout)
	for {
		select {
		case <-deadline:
			return nil, errors.New("the unlock prompt was not answered")
		case sig, ok := <-signals:
			if !ok {
				return nil, errors.New("the session bus closed during the unlock prompt")
			}
			if sig.Path != prompt || sig.Name != promptIface+".Completed" || len(sig.Body) < 2 {
				continue
			}
			if dismissed, _ := sig.Body[0].(bool); dismissed {
				return nil, errors.New("unlock was dismissed")
			}
			if v, ok := sig.Body[1].(dbus.Variant); ok {
				if paths, ok := v.Value().([]dbus.ObjectPath); ok {
					return paths, nil
				}
			}
			return opened, nil
		}
	}
}

// ------------------------------------------------------------------ KWallet

// KWallet is where Chromium keeps the password on KDE when it does not use
// the Secret Service: in the network wallet, under the folder "<name> Keys"
// and the entry "<name> Safe Storage". Plasma 6 runs kwalletd6, Plasma 5
// kwalletd5; the interface is the same.
const (
	kwalletIface = "org.kde.KWallet"
	kwalletAppID = "youtuimusic"
)

var kwalletDaemons = []struct{ bus, path string }{
	{"org.kde.kwalletd6", "/modules/kwalletd6"},
	{"org.kde.kwalletd5", "/modules/kwalletd5"},
}

// kwalletPasswords returns the password stored under the first of names that
// has one.
func kwalletPasswords(names []string) ([][]byte, error) {
	conn, err := sessionBus()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	var errs []error
	for _, d := range kwalletDaemons {
		pw, err := kwalletRead(conn.Object(d.bus, dbus.ObjectPath(d.path)), names)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return [][]byte{pw}, nil
	}
	return nil, oneLine(errs)
}

func kwalletRead(wallet dbus.BusObject, names []string) ([]byte, error) {
	quick := func(method string, args ...any) *dbus.Call {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return wallet.CallWithContext(ctx, kwalletIface+"."+method, 0, args...)
	}

	var name string
	if err := quick("networkWallet").Store(&name); err != nil {
		return nil, err
	}
	// Opening a closed wallet asks for its password, so this one call gets
	// the prompt's allowance.
	ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
	defer cancel()
	var handle int32
	if err := wallet.CallWithContext(ctx, kwalletIface+".open", 0, name, int64(0), kwalletAppID).
		Store(&handle); err != nil {
		return nil, err
	}
	if handle < 0 {
		return nil, fmt.Errorf("wallet %q was not opened", name)
	}
	defer quick("close", handle, false, kwalletAppID)

	for _, n := range names {
		var pw string
		if err := quick("readPassword", handle, n+" Keys", n+" Safe Storage", kwalletAppID).
			Store(&pw); err == nil && pw != "" {
			return []byte(pw), nil
		}
	}
	return nil, fmt.Errorf("no password for %q in wallet %q", names, name)
}

// ----------------------------------------------------------------- Keychain

// keychainPasswords reads the password from the macOS login Keychain, where
// Chromium files it as the generic password "<name> Safe Storage" under the
// account "<name>". The first read asks the user to allow access; that is
// macOS's prompt, and the reason this gets promptTimeout.
func keychainPasswords(names []string) ([][]byte, error) {
	var errs []error
	for _, n := range names {
		ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
		out, err := exec.CommandContext(ctx, "security", "find-generic-password",
			"-w", "-a", n, "-s", n+" Safe Storage").Output()
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s Safe Storage: %w", n, err))
			continue
		}
		if pw := bytes.TrimRight(out, "\r\n"); len(pw) > 0 {
			return [][]byte{pw}, nil
		}
	}
	return nil, oneLine(errs)
}

// oneLine joins errors with semicolons rather than errors.Join's newlines,
// since these end up inside a sentence.
func oneLine(errs []error) error {
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		msgs = append(msgs, err.Error())
	}
	return errors.New(strings.Join(msgs, "; "))
}
