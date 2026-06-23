// @sextant/conv-review — the review convention in TypeScript (ADR-0044). An
// artifact carries a review-state as a `review` block in its record; the operator's
// verdict is persisted by a read-merge-CAS, and on approve a closed loop advances
// any proof-related goal criteria to met via @sextant/conv-goals. This is the
// dashapi review logic moved into a convention library so the browser dash runs it
// directly over its own bus Client, with no Go-backend re-implementation.
//
// A convention is a library over the SDK (ADR-0041), never a bus feature: the verb
// LOGIC here is hand-written (concept, not codegen) and reaches the bus only
// through the Ops seam. A Go peer (clients/go/conventions/review) is a follow-up to
// complete ADR-0041 co-equality; this TS convention is the live home for now.

export {
  type Ops,
  type ReviewState,
  type ReviewBlock,
  type SetReviewInput,
  type SetReviewResult,
  type AdvancedCrit,
  REVIEW_STATES,
  isReviewState,
  setReview,
  closeLoop,
  mergeReview,
  ReviewError,
} from "./review.js";
