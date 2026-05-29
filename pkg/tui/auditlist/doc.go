// Package auditlist is the Tier 1 component for `sextant audit list -i`:
// a ListPane over the query_audit RPC (last 24h, filterable). Enter
// emits an audit-detail open intent. Per the RFC (P2). The live
// `audit tail` stream surface reuses the daemon-logs StreamViewport
// pattern and is tracked separately.
package auditlist
