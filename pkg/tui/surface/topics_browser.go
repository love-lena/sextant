package surface

import (
	"context"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/tui/busfeed"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// topicDiscoverySubject is the wildcard the topics browser subscribes to in order
// to discover topics client-side (ADR-0024): every subject under the topic space.
// A topic exists because it has messages (ADR-0012) — there is no registry — so
// the browser derives the list from its own subscription, demultiplexing the
// distinct topic segments out of the frames it sees.
const topicDiscoverySubject = sx.MessagePrefix + "topic.>"

// topicSubjectPrefix is the segment a discovered subject carries before its topic
// name (msg.topic.<name>). The topic is everything after it.
const topicSubjectPrefix = sx.MessagePrefix + "topic."

// TopicsBrowser is the topics browser (ADR-0024): a live list of every topic that
// has messages, which opens the conversation for the selected one in place. There
// is no topic registry — a topic exists because it has messages (ADR-0012) — so
// the list is discovered CLIENT-SIDE: the browser subscribes to msg.topic.>
// (replaying history) and demultiplexes the distinct topic segments out of the
// frames it sees, so the list grows live as topics first carry a message. Enter
// opens a Stream on the selected topic's subject (sx.TopicSubject(name)) — the
// same conversation surface a DM opens, over a topic subject.
//
// SCALE CAVEAT: the discovery feed replays ALL topic history just to learn the
// set of topic NAMES — it reads every retained message on every topic only to
// extract subjects, discarding the bodies. This is fine at M4 scale (a handful of
// topics, modest history) but does not scale: a future message.topics read verb
// (the artifact.list shape, ADR-0024) would return the names directly without
// replaying the messages. Until then, discovery is this subscription.
//
// It embeds Browser and owns the discovery feed (a resource beyond the open
// detail), so it overrides Stop to tear the feed down in addition to the detail.
type TopicsBrowser struct {
	Browser

	client *sextant.Client
	ctx    context.Context

	feed *busfeed.Feed

	// topics is the set of discovered topic names. The list rows are the sorted
	// set, rebuilt on each newly-seen topic so the list grows live.
	topics map[string]bool
	// names holds the topic name for each list row, in the same sorted order as the
	// rows, so openRow resolves the selected topic by cursor index.
	names []string
	// err holds a discovery-feed error for the footer (fail-loud), kept honest
	// rather than swallowed.
	err error
	// authors resolves frame author ids in every conversation the browser opens
	// (WithConversationAuthors); nil renders the documented short-id fallback.
	authors map[string]Author
}

// TopicsOption configures a TopicsBrowser.
type TopicsOption func(*TopicsBrowser)

// WithConversationAuthors supplies the id → Author map every conversation the
// browser opens renders authors with (display name in role hue instead of a raw
// id). The topics browser has no directory of its own — topics are discovered
// from subjects, which carry no author identities — so the host resolves the map
// (from clients.list) and threads it in here; the same seam ADR-0023 leaves the
// standalone Stream (WithAuthors).
func WithConversationAuthors(authors map[string]Author) TopicsOption {
	return func(t *TopicsBrowser) { t.authors = authors }
}

// NewTopicsBrowser builds a topics browser over client. Pass a context that lives
// as long as the browser (it scopes the discovery subscription and each opened
// conversation's feed) and the resolved theme/keymap. The browser does no I/O
// until Init.
func NewTopicsBrowser(ctx context.Context, client *sextant.Client, th theme.Theme, keys theme.Keymap, opts ...TopicsOption) *TopicsBrowser {
	t := &TopicsBrowser{
		client: client,
		ctx:    ctx,
		feed:   busfeed.New(client, topicDiscoverySubject, sextant.DeliverAll()),
		topics: map[string]bool{},
	}
	t.Browser = newBrowser("topics", "Topics", keys, th, func(cursor int) (Surface, string) {
		if cursor < 0 || cursor >= len(t.names) {
			return nil, ""
		}
		name := t.names[cursor]
		// Enter opens the topic's conversation — the same Stream surface a DM opens,
		// over the topic subject rather than a client subject.
		s := NewStream(t.ctx, t.client, sx.TopicSubject(name), t.th, t.keys,
			WithCompose(), WithAuthors(t.authors))
		return s, "Topic · " + name
	})
	for _, o := range opts {
		o(t)
	}
	return t
}

// SetTheme re-themes the browser: the list rows bake in the kind hue at rebuild
// time, so a runtime theme switch rebuilds them from the discovered set (the
// embedded Browser re-themes itself and any open detail).
func (t *TopicsBrowser) SetTheme(th theme.Theme) {
	t.Browser.SetTheme(th)
	t.rebuild()
}

// Init opens the discovery feed. The pump runs from Update: every EventMsg and
// DroppedMsg re-issues Next, mirroring the stream surface's pump.
func (t *TopicsBrowser) Init() tea.Cmd {
	return t.feed.Subscribe(t.ctx)
}

// Update drives the discovery-feed pump (learning topic names from the frames it
// replays), then delegates to Browser.Update for navigation and detail delegation.
// A discovered topic is added to the set and the rows rebuilt, so the list grows
// live as topics first carry a message.
//
// Two feeds can be live here at once — the discovery wildcard and an opened
// conversation's own subscription — and their messages are the same types, so
// the browser demultiplexes on the From tag: it claims only its discovery feed's
// messages and hands everything else to Browser.Update, which routes it to the
// open detail. (Without the demux, the detail's SubscribedMsg would be answered
// with the DISCOVERY feed's Next — a pump that blocks forever once the replay is
// drained — and the conversation would never start.) An untagged busfeed message
// (nil From — test-synthesized) is claimed by discovery only at the list, where
// there is no detail it could have been meant for.
func (t *TopicsBrowser) Update(msg tea.Msg) tea.Cmd {
	if t.claims(msg) {
		switch msg := msg.(type) {
		case busfeed.SubscribedMsg:
			// Subscription is live; start the pump.
			return t.feed.Next()
		case busfeed.EventMsg:
			t.observe(msg.Message)
			return t.feed.Next() // keep pumping
		case busfeed.DroppedMsg:
			// A dropped discovery frame only risks missing a topic name until its next
			// message; not terminal, so keep pumping (no gap marker — the list is a set,
			// not a stream).
			return t.feed.Next()
		case busfeed.ErrMsg:
			// Terminal: the discovery feed stops reading. Surface the error.
			t.err = msg.Err
			return nil
		}
	}
	return t.Browser.Update(msg)
}

// claims reports whether a busfeed message belongs to the discovery feed: tagged
// by it, or untagged (test-synthesized) while the list is showing — at the list
// there is no open detail the message could have been meant for.
func (t *TopicsBrowser) claims(msg tea.Msg) bool {
	if !isBusfeedMsg(msg) {
		return false
	}
	from := busfeed.From(msg)
	return from == t.feed || (from == nil && !t.inDetail())
}

// isBusfeedMsg reports whether msg is one of the busfeed message types (tagged or
// not). The discovery demux needs it to tell "an untagged busfeed message" apart
// from every other message Browser.Update should see.
func isBusfeedMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case busfeed.SubscribedMsg, busfeed.EventMsg, busfeed.DroppedMsg, busfeed.ErrMsg:
		return true
	}
	return false
}

// View renders the list (or the open detail) with a discovery-feed error footer
// below it when one is showing — kept visible rather than swallowed (fail-loud).
// At the detail level the inner surface owns its own footer, so the discovery
// error only shows at the list.
func (t *TopicsBrowser) View() string {
	body := t.Browser.View()
	if t.err != nil && !t.inDetail() {
		return body + "\n" + errorFooter(t.th, t.err, t.width())
	}
	return body
}

// Stop tears the discovery feed down (ending its blocked Next pump) and tears down
// any open detail. The topics browser owns the feed beyond the open detail, so it
// overrides Stop to release both (the Surface contract's teardown). It is safe to
// call more than once.
func (t *TopicsBrowser) Stop() {
	t.feed.Stop()
	t.stopDetail()
}

// observe learns a topic name from one discovered frame: it extracts the topic
// segment from the frame's subject and, if new, adds it to the set and rebuilds
// the rows. A frame outside the topic space (no msg.topic. prefix) or with an
// empty name is ignored.
func (t *TopicsBrowser) observe(m sextant.Message) {
	name := topicOf(m.Subject)
	if name == "" || t.topics[name] {
		return
	}
	t.topics[name] = true
	t.rebuild()
}

// rebuild sets the list rows from the sorted topic set, recording each row's topic
// name in the parallel names slice so Enter resolves by index.
func (t *TopicsBrowser) rebuild() {
	names := make([]string, 0, len(t.topics))
	for name := range t.topics {
		names = append(names, name)
	}
	sort.Strings(names)

	items := make([]widget.ListItem, len(names))
	for i, name := range names {
		items[i] = widget.ListItem{
			Title: name,
			Hue:   t.th.KindHue(theme.KindChat),
		}
	}
	t.names = names
	t.setRows(items)
}

// topicOf returns the topic name carried by a subject (the segment after
// msg.topic.), or "" for a subject outside the topic space or with no name. A
// nested subject (msg.topic.artifact.foo — an artifact comment thread) keeps its
// full trailing path as the topic name, so it appears as its own row rather than
// collapsing into a parent.
func topicOf(subject string) string {
	if !strings.HasPrefix(subject, topicSubjectPrefix) {
		return ""
	}
	return subject[len(topicSubjectPrefix):]
}

// width returns the inner width for the error footer: the width the embedded
// Browser recorded on the last SetSize.
func (t *TopicsBrowser) width() int { return t.w }
