# spawn/ts — reserved

The TS peer of the spawn convention. The Go side (`conventions/spawn/go`) is the
existing contract, split out of the dispatcher; this TS slot is **reserved, not
yet built**.

Implementing it (the TS convention + spawn conformance vectors, co-equal with the
Go side) is **TASK-239**. The ADR-0049 restructure only relocates the existing
sides and reserves the peers, so it stays purely behaviour-preserving.
