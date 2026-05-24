package natsboot

import (
	"fmt"
	"io"
	"strconv"
)

// renderConfig writes a nats-server .conf representation of cfg to w.
// The format is NATS' native key/value syntax. We intentionally hand-roll
// the renderer rather than pulling in the nats-server library so natsboot
// can stay minimal and the output stays human-inspectable.
//
// cfg must be the result of Config.validateAndFill — no defaulting is
// performed here.
func renderConfig(w io.Writer, cfg Config) error {
	// All values that hold a path or user-controlled string get
	// quote-escaped so embedded quotes/newlines cannot break the
	// generated file.
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	}

	if err := write("server_name: %s\n", quoteString(cfg.ServerName)); err != nil {
		return err
	}
	if err := write("listen: %s\n", quoteString(cfg.ListenHost+":"+strconv.Itoa(cfg.ListenPort))); err != nil {
		return err
	}
	// nats-server v2.14 runs in strict-config mode and rejects unknown
	// keys. Stick to the minimum set: server_name, listen, jetstream,
	// authorization. Logging defaults (stderr) are fine; LogFile in the
	// Config redirects stdout/stderr at the process level instead.
	if err := write("jetstream {\n  store_dir: %s\n}\n", quoteString(cfg.DataDir)); err != nil {
		return err
	}
	if err := write("authorization {\n  users = [\n    { user: %s, password: %s, permissions: { publish: \">\", subscribe: \">\" } }\n  ]\n}\n",
		quoteString(cfg.OperatorUser),
		quoteString(cfg.OperatorPassword),
	); err != nil {
		return err
	}
	return nil
}

// quoteString returns s wrapped in double-quotes with embedded quotes and
// backslashes escaped. NATS' config parser accepts the standard
// JSON-like quoting we need.
func quoteString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '"':
			out = append(out, '\\', c)
		case '\n':
			out = append(out, '\\', 'n')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, c)
		}
	}
	out = append(out, '"')
	return string(out)
}
