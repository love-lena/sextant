// Package conventions is the home for the Go convention libraries (ADR-0041).
//
// A convention — "set a goal", "request a review", "curate the home" — is not a
// bus feature and not a shared engine. Each is a library over the SDK
// (sdk/go) that turns a domain action into a sequence of the same
// primitive operations a bare client could issue: engine-as-a-library, content
// stays opaque to the bus. The bright line is mechanical — importcheck's
// AssertConventionDeps holds a convention's production closure to the SDK and
// the protocol bindings and forbids the bus (conventions/ never
// reaches bus/).
//
// This package is a placeholder marking the directory: it has no exported
// surface yet. The first convention library (goals) lands in a later ticket;
// until then the directory exists so the tree reads as the architecture and the
// import-direction check has a target.
package conventions
