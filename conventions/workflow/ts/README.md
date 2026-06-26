# workflow/ts — reserved

The TS peer of the workflow convention. The Go side (`conventions/workflow/go`) is
the existing contract, split out of the coordinator; this TS slot is **reserved,
not yet built**.

Implementing it (the TS convention + workflow conformance vectors, co-equal with
the Go side) is **TASK-239**. The ADR-0049 restructure only relocates the existing
sides and reserves the peers, so it stays purely behaviour-preserving.
