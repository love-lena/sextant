// Package templates loads agent templates from disk and from NATS KV.
//
// Templates are the spawning recipes per specs/architecture.md §11b. Each
// is a TOML file under `~/.config/sextant/templates/` (`sextant init`
// seeds the default). The spawn handler (M11) resolves templates by name
// from the `templates` NATS KV bucket; sextantd seeds that bucket from
// the on-disk files at startup. For initial, re-running `sextant init`
// is the reload path.
//
// Plan: plans/bootstrap.md#M11
package templates
