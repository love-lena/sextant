---
title: Build chat TUI — modal one-agent chat window for `sextant conversation <agent>`
status: resolved
priority: P2
created_at: 2026-05-25T16:23-07:00
resolved_at: 2026-05-26T17:45-07:00
labels: [feature, tui, cli, ergonomics, operator-experience]
discovered_in: design handoff "Sextant Chat" (v0.4-draft)
---

## Resolution

Shipped on branch `worktree-feat-chat-tui-impl`. Implementation plan at `plans/feat-chat-tui-impl.md`. Package: `pkg/tui/chat/`. Wired into `cmd/sextant/conversation.go` (TUI is default, `--json` keeps NDJSON, `--read` opens read-only variant). 35 unit tests in the chat package, 3 dispatch tests in `cmd/sextant`. All acceptance tests from the original spec are covered.

Follow-up tickets filed during Checkpoint C live testing — see `bug-agents-list-stale-lifecycle`, `bug-prompt-agent-accepts-when-sidecar-gone`, `bug-sidecar-nats-disconnect-no-reconnect`, `bug-sidecar-queued-prompt-drain-orphans-context`, `feat-chat-tui-history`, `feat-chat-tui-status-dot`, `feat-sextant-agents-check`, `feat-sextant-doctor-agents`, `feat-ask-conversation-self-diagnose-on-timeout`, `feat-sextant-help-flags-per-subcommand`, `feat-sextant-cobra-fang-migration`, `feat-sextant-tail-duration-flag`.

---

## Summary

Today `sextant conversation <uuid>` prints frames + lifecycle envelopes to stdout in a forever-live tail. It works for piping to a file or eyeballing a single turn, but it's not what an operator wants when they're actually *talking* to an agent: scroll-back is terminal-managed, there's no way to compose a reply from inside the view, and the rendering doesn't distinguish user/agent turns or attach tool calls to the turn that emitted them.

Build a proper chat TUI: a standalone window for one agent that an operator pins in a terminal pane next to whatever else they're watching (agent list, audit log, editor). No menu bar, no jumps to other surfaces — just this agent's frame stream and the operator's voice into it.

Design reference: `Sextant Chat.html` (v0.4-draft). The mockup is **grayscale wireframe** — it specifies structure, hierarchy, interaction states, and key bindings, **not** visual treatment. Visual layer comes from the [charmbracelet](https://github.com/charmbracelet) toolkit (bubbletea + lipgloss + bubbles); embrace that design language rather than translating the mockup's CSS pixel-for-pixel.

## Scope

### MVP (Iteration 4 — "Modal")

Two modes, vim-flavored. This is the architectural call — get NORMAL / INSERT plumbed and the rest (search, slash commands, permission acks) lands as extensions of NORMAL-mode keys.

**NORMAL mode** (default on launch)
- Cursor lives in the stream. The selected turn is highlighted (left-border accent + tinted row background).
- `j` / `k` (and arrow keys) — step turns one at a time.
- `gg` / `G` — top / bottom.
- Selection-centered scroll: the selected turn lands in the middle of the viewport when navigation moves it off-screen.
- Composer is **parked**: dimmed (~55% opacity), no caret, draft preserved, "`i` to edit" hint on the right.
- Stream auto-tails when the selection is on the last turn AND no manual navigation has moved off it; otherwise scroll position holds.

**INSERT mode**
- Cursor physically moves into the composer: border lights up (active accent), caret blinks, status pill flips, stream snaps to the bottom (live).
- `↵` — send: dispatches the draft via `prompt_agent` RPC, clears the composer, returns to NORMAL with selection on the just-sent (newest) turn.
- `⇧↵` — newline inside the draft. Multi-line drafts preserved across mode switches.
- `Esc` — back to NORMAL. Draft is preserved.
- While in INSERT, j/k/gg/G are characters in the draft, not navigation — that's the whole point of the modal split.

**Mode-aware status bar** (bottom)
- Mode pill on the left: `NORMAL` (default-accent, outlined) / `INSERT` (active-accent, filled).
- Only the keys that work *in the current mode* are shown — no busy legend of inert hotkeys.
- Right side: `turn N / total` in NORMAL, `<total> turns · live` in INSERT.

**Header** (top, ~38px tall in the mockup; one row in lipgloss terms)
- Status dot (pulses when the agent has pending lifecycle attention).
- Agent name (bold) · branch ref (muted, prefixed with `⎇`).
- Pending-permission badge on the right when count > 0 (outlined pill: `N pending`).

**Stream rendering** (the body)
- Each turn is a row with three columns: time (right-aligned, muted, narrow), actor (bold, fixed-width), content (flex). Glyph in front of actor distinguishes user (`›`) from agent (`●`).
- Tool calls render *attached* to the agent turn that emitted them — one line per tool call, indented under the turn, with `⚡ <tool> [<arg>] <status> · <duration>`. They are not separate turns.
- Selected turn: accent left-border (4-char-wide bar / lipgloss `BorderLeft`), tinted background.
- Scroll-position thumb on the right edge when content exceeds viewport.

### Also ship: `--read` variant (Iterations 1–2)

`sextant conversation <agent> --read` opens the same TUI without a composer. Iteration 1 (no keyboard) and 2 (read-nav with j/k/gg/G + selection) are the same code path with the composer hidden and INSERT mode disabled. Use cases: pinning an agent in a side pane while you work elsewhere; pairing with a co-worker who needs to see what an agent did; running on a shared display.

The READ pill renders `READ` (muted) in iter-1 mode, sage in iter-2; we'll ship iter-2 since the read-nav cost is near-zero on top of the MVP.

### Out of scope (explicitly rejected): always-on composer

Iteration 3 in the mockup — composer always focused, no modes, chat-app-style. **Do not build this.** The design doc records the reasoning:

- Every keypress lands in the composer — no room for navigation hotkeys.
- Breaks the muscle memory already trained in the other sextant TUIs (audit, agents, pending — all vim-flavored).
- Scroll position is fragile: a stray key, lose your place.
- No way to ack a permission without typing `/approve` or similar (slower than a single key — and permission acks are a deferred feature that depends on free hotkeys).
- Feels off-pattern next to vim / zellij / k9s — the tools operators sit beside.

This is documented so the choice is explicit and the idea doesn't quietly resurface during implementation.

## Implementation shape

New package: `pkg/tui/chat/` (this is the first TUI in the repo; the package layout sets the precedent — co-locate further per-surface TUIs as siblings of `chat/`).

- `model.go` — bubbletea `Model` holding: mode, turns (`[]Turn`), selection index, viewport scroll state, composer draft buffer (multi-line), agent metadata (name, branch, pending count), connection status, NATS subscription handles.
- `update.go` — `Update(msg tea.Msg) (tea.Model, tea.Cmd)` switching on key messages by mode and on inbound frame/lifecycle messages.
- `view.go` — composes header + stream + composer + status bar with lipgloss. Lean on `bubbles/viewport` for the stream and `bubbles/textarea` for the composer.
- `keys.go` — `key.Binding` set per mode, also drives the help/status footer (`bubbles/key.Help` rendering of the bound keys for the current mode).
- `style.go` — central lipgloss styles (header, turn, selected-turn, tool-line, composer-parked, composer-active, status-pill-normal, status-pill-insert, etc.). All accents come from named role tokens (`activeBorder`, `selectMark`, `attention`, `destructive`) — *not* hard-coded hex — so the visual treatment can evolve without touching layout code.
- `frames.go` — wraps the existing frame/lifecycle subscription into a `tea.Cmd` that emits typed messages; reuses the renderer logic from `cmd/sextant/conversation.go` for actor/text/tool extraction.

Wire into `cmd/sextant/conversation.go`:
- Default (no `--json`): launch the TUI. Existing `--from-seq` flag continues to work (seeds the stream).
- `--json`: keep the current NDJSON streamer.
- `--read`: same TUI, composer disabled, INSERT mode unreachable.
- `--tail`: current behavior — exit on `lifecycle transition=ended`. In the TUI, this means the window closes itself after rendering the terminal lifecycle envelope.

The existing `--from-seq` and `--json` behaviors stay byte-identical for piped consumers.

## Design system

Per the handoff: "embrace charm's design language" — that's [bubbletea](https://github.com/charmbracelet/bubbletea), [lipgloss](https://github.com/charmbracelet/lipgloss), [bubbles](https://github.com/charmbracelet/bubbles), and (for any markdown rendering inside turns) [glamour](https://github.com/charmbracelet/glamour).

Reusable conventions to establish here (since this is the first sextant TUI):

- **Mode pill** at the status bar is the canonical mode indicator across all future sextant TUIs. Filled background for "active-input" modes (INSERT, CHAT), outlined for navigational ones (NORMAL, READ).
- **Role tokens** (not raw colors) drive accents: `activeBorder` (focused surface), `selectMark` (this row is selected), `attention` (needs ack), `destructive` (dangerous). Define these as lipgloss color values in one place and reuse — every future TUI inherits the same vocabulary.
- **Vim-flavored navigation**: j/k/gg/G/i/Esc/↵ become the shared baseline. Slash commands (`/`), search (`/...`), and yank/paste land as extensions.
- **Selection-centered scroll**: when the selection moves off-screen, scroll to put it back in the middle. Don't snap to top/bottom unless explicitly gg/G.

## Open questions (resolve during implementation)

From the design handoff — answer in code, not paper:

1. **Initial mode on launch** — NORMAL with last turn selected (current spec, vim-style), or INSERT with empty draft (chat-app-style)? Ship NORMAL by default; revisit after a week of daily-drive.
2. **Extra exit keys for INSERT** — should `⌃c` / `⌃[` also work as Esc? They're muscle memory for vim users, but `⌃c` may collide with terminal kill in some setups. Ship Esc-only; add the others behind a config flag if requested.
3. **Where does ↵ leave you?** Current spec: bounce to NORMAL at newest turn ("send, return to base"). Alternative: stay in INSERT (Slack-like). Ship the bounce — it makes the mode unambiguous.
4. **Streaming-token assistant text** — when the agent is mid-reply, render tokens as they arrive with a live cursor on the last frame? Deferred (see below) but the model should be designed so a partial-frame state slots in without restructuring.
5. **Scroll-anchoring on new frames** — if operator has scrolled up in NORMAL and a new frame arrives, hold position (current) or snap to it? Hold position; surface an "N newer · `G` to jump" hint in the status bar as a future enhancement.

## Deferred (post-MVP, each is its own follow-up issue)

Designed and discarded for the MVP cut. Each is intentionally absent so the modal pattern lands cleanly first.

- **Permission requests** — agent pauses on a tool that needs ack. Inline attention-accent card at the frame; `a` / `d` / `e` hotkeys in NORMAL.
- **Slash commands** — `/approve`, `/deny`, `/attach`, `/diff`, `/clear`, `/history`. Palette pops above composer in INSERT.
- **Inline diff expand** — tool calls render one-line by default; `↵` in NORMAL on a tool row expands the diff inline.
- **Search** — `/` enters search mode; matches highlight in stream; `n` / `N` to step.
- **Attachments** — paste a diff, drop a file ref, pin a frame to the composer.
- **Trace IDs + seq** — surface frame seq numbers and trace IDs in a metadata footer per frame.
- **Thinking blocks** — collapsed by default; `↵` on a thinking row expands the chain-of-thought.
- **Streaming tokens** — live cursor on the in-flight assistant frame; tokens arrive incrementally.

## Acceptance

`TestChatTUIRendersTurnsAndAttachesToolCalls`:
1. Seed the model with a fixture transcript including user turns, agent turns, and tool-call frames.
2. Render the view to a string with lipgloss in a fixed-width terminal.
3. Assert: each turn is one row with time/actor/content; tool-call frames render *under* the agent turn they belong to, not as separate rows; selected turn has the accent left-border.

`TestChatTUIModeTransitions`:
1. Boot the model in NORMAL with selection at the last turn.
2. Press `j` — selection clamps at the last index (no advance past end).
3. Press `k` — selection moves up one.
4. Press `i` — mode = INSERT, composer becomes active, status pill flips.
5. Type "hello", press `↵` — model emits a `SendDraft("hello")` command, draft clears, mode = NORMAL, selection = newest turn.
6. Press `i`, type "more", press `Esc` — mode = NORMAL, draft = "more" (preserved).

`TestChatTUIReadModeDisallowsInsert`:
1. Boot the model with `read=true`.
2. Press `i` — mode stays NORMAL, no composer rendered, status pill reads `READ`.

`TestSextantConversationLaunchesTUIByDefault`:
1. `sextant conversation <uuid>` — TUI starts (asserted by checking that the bubbletea program is spawned, not the raw NDJSON streamer).
2. `sextant conversation <uuid> --json` — current NDJSON behavior, no TUI.
3. `sextant conversation <uuid> --read` — TUI starts with `read=true` propagated.

`TestChatTUIPreservesDraftAcrossModeFlips`:
1. NORMAL → `i` → type "hold on" → `Esc` → assert draft still "hold on" → `i` → type " — wait" → assert draft "hold on — wait" → `↵` → assert send command carries "hold on — wait" and draft clears.

`TestChatTUIToolCallStatusColor`:
1. Tool call with `status="ok"` and `status="failed"` (or non-empty error).
2. Assert the renderer applies the success accent to the former and the destructive accent to the latter — via role tokens, not direct hex.

## Related

- `cmd/sextant/conversation.go` — existing NDJSON streamer; the TUI wraps this same subscription pipeline.
- `cmd/sextant/ask.go` — already does the subscribe-then-publish dance synchronously; the TUI's send path follows the same RPC contract (`prompt_agent`).
- `[[bug-lifecycle-turn-ended-missing]]` (resolved) — the status bar's "live" indicator depends on lifecycle envelopes being emitted reliably.
- `Sextant Chat.html` — the design handoff doc (v0.4-draft). Treat as structure-and-behavior reference, not pixel spec.
- `zls-chrome.jsx` (referenced in the handoff) — sibling design file with the canonical color tokens; not in this repo, ask the designer for the values when wiring `style.go`.

## Why P2 not P1

Existing `sextant conversation` works for piping and one-shot inspection. The TUI is a major ergonomic upgrade and the first piece of the broader TUI program (audit, agents, pending, worktrees, dashboard — each one of those will follow the conventions established here), so it deserves prompt attention. But it doesn't block any agent operation or fix a correctness bug, so it's not P1.
