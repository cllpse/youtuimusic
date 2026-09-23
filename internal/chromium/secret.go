package chromium

import (
	"errors"
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
)

// The Secret Service API, which gnome-keyring and KeePassXC both implement.
// Chromium stores the password that encrypts its cookies here, under an
// attribute naming the application.
const (
	secretBus    = "org.freedesktop.secrets"
	secretRoot   = "/org/freedesktop/secrets"
	serviceIface = "org.freedesktop.Secret.Service"
	itemIface    = "org.freedesktop.Secret.Item"
	promptIface  = "org.freedesktop.Secret.Prompt"
)

// ErrNoKeyring means the password could not be retrieved. Cookies written
// with the v10 prefix do not need it, so this is not always fatal.
var ErrNoKeyring = errors.New("chromium: no Secret Service password available")

// secretValue is the wire shape returned by Item.GetSecret.
type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// keyringPassword fetches a browser's storage password from the Secret
// Service. app is the value Chromium writes into the "application"
// attribute — "chromium", "chrome", "brave" and so on.
func keyringPassword(app string) ([]byte, error) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		return nil, fmt.Errorf("%w: no session bus", ErrNoKeyring)
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoKeyring, err)
	}
	defer func() { _ = conn.Close() }()

	svc := conn.Object(secretBus, secretRoot)

	// "plain" means the secret crosses the bus unencrypted. The alternative
	// is a DH handshake, which buys nothing here: the bus is the user's own
	// session, and anything that can read it can read the cookie file too.
	var output dbus.Variant
	var session dbus.ObjectPath
	if err := svc.Call(serviceIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).
		Store(&output, &session); err != nil {
		return nil, fmt.Errorf("%w: OpenSession: %v", ErrNoKeyring, err)
	}

	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(serviceIface+".SearchItems", 0, map[string]string{"application": app}).
		Store(&unlocked, &locked); err != nil {
		return nil, fmt.Errorf("%w: SearchItems: %v", ErrNoKeyring, err)
	}

	if len(unlocked) == 0 && len(locked) > 0 {
		opened, err := unlock(conn, svc, locked)
		if err != nil {
			return nil, err
		}
		unlocked = opened
	}
	if len(unlocked) == 0 {
		return nil, fmt.Errorf("%w: no stored password for %q", ErrNoKeyring, app)
	}

	for _, item := range unlocked {
		var s secretValue
		if err := conn.Object(secretBus, item).Call(itemIface+".GetSecret", 0, session).
			Store(&s); err != nil {
			continue
		}
		if len(s.Value) > 0 {
			return s.Value, nil
		}
	}
	return nil, fmt.Errorf("%w: every stored password for %q was empty", ErrNoKeyring, app)
}

// unlock asks the Secret Service to unlock items, following the prompt the
// service may return. A locked keyring is the ordinary state after a login
// that did not unlock it, so this is a normal path rather than an error.
func unlock(conn *dbus.Conn, svc dbus.BusObject, locked []dbus.ObjectPath) ([]dbus.ObjectPath, error) {
	var opened []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := svc.Call(serviceIface+".Unlock", 0, locked).Store(&opened, &prompt); err != nil {
		return nil, fmt.Errorf("%w: Unlock: %v", ErrNoKeyring, err)
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
		return nil, fmt.Errorf("%w: prompt match: %v", ErrNoKeyring, err)
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	if call := conn.Object(secretBus, prompt).Call(promptIface+".Prompt", 0, ""); call.Err != nil {
		return nil, fmt.Errorf("%w: Prompt: %v", ErrNoKeyring, call.Err)
	}
	for sig := range signals {
		if sig.Path != prompt || sig.Name != promptIface+".Completed" || len(sig.Body) < 2 {
			continue
		}
		if dismissed, _ := sig.Body[0].(bool); dismissed {
			return nil, fmt.Errorf("%w: unlock was dismissed", ErrNoKeyring)
		}
		if v, ok := sig.Body[1].(dbus.Variant); ok {
			if paths, ok := v.Value().([]dbus.ObjectPath); ok {
				return paths, nil
			}
		}
		return opened, nil
	}
	return nil, fmt.Errorf("%w: prompt never completed", ErrNoKeyring)
}
