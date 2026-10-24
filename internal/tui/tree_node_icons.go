package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// tree_node_icons.go is the Object Explorer's glyphs: nodeIcon, which picks
// one for a node from its type and state in a style (Emoji, Symbols,
// Portable), and the substitutions a node's state makes (offline database,
// stopped session, publication kind). Each type's own glyphs, and whether it
// is a folder, are nodeKinds' columns (tree_node_kinds.go).

// nodeIcon returns the icon glyph for a node, in the given icon style.
// expanded only affects container ("folder") node types, which show a
// different glyph open vs. closed. style == config.IconStyleNone always
// returns 0 (no icon), which TreeView's Draw treats as "don't draw one".
// NodeAgentJobs (the "SQL Server Agent" node) gets a fixed stopwatch glyph
// rather than the generic folder icon its container status would give it.
// d.IsPrimaryKey overrides the normal NodeColumn glyph with the primary-key
// glyph, since that's per-column data, not something Type alone expresses.
func nodeIcon(d nodeData, style config.IconStyle, expanded bool) rune {
	if style == config.IconStyleNone {
		return 0
	}
	if d.Type == NodeAgentJobs {
		return '⏱'
	}
	if isContainerNode(d.Type) {
		return folderIcon(style, expanded)
	}
	if d.Type == NodeColumn && d.IsPrimaryKey {
		return primaryKeyIcon(style)
	}
	if d.Type == NodeDatabase && d.IsOffline {
		return offlineDatabaseIcon(style)
	}
	if d.Type == NodeEventSession && !d.IsEnabled {
		return stoppedEventSessionIcon(style)
	}
	if d.Type == NodePublication {
		return publicationIcon(d.ReplPubType, style)
	}
	return objectIcon(d.Type, style)
}

// publicationIcon returns a NodePublication's glyph by its kind, as SSMS
// draws each kind its own: objectIcon's NodePublication glyph is the
// transactional one, and the other three substitute here.
func publicationIcon(t gosmo.PublicationType, style config.IconStyle) rune {
	emoji := style == config.IconStyleEmoji
	switch t {
	case gosmo.PublicationSnapshot:
		if emoji {
			return '📷'
		}
		return '◘'
	case gosmo.PublicationPeerToPeer:
		if emoji {
			return '🔁'
		}
		return '⇋'
	case gosmo.PublicationMerge:
		if emoji {
			return '🔀'
		}
		return '⋔'
	}
	return objectIcon(NodePublication, style)
}

// stoppedEventSessionIcon returns the glyph substituted for a NodeEventSession
// that is not running — the hollow twin of the running one, the same idiom as
// offlineDatabaseIcon. The label says nothing about the state, as SSMS's
// doesn't: a session list is mostly stopped sessions, and a suffix on each
// would crowd out the names.
func stoppedEventSessionIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '⏹'
	}
	return '✧'
}

// offlineDatabaseIcon returns the glyph substituted for a NodeDatabase
// that's currently offline — a hollow hexagon in the geometric styles
// (vs. the filled '⬢' an online database uses), a "powered off" glyph
// for Emoji.
func offlineDatabaseIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '📴'
	}
	return '⬡'
}

// primaryKeyIcon returns the glyph substituted for a primary-key column's
// normal NodeColumn icon — the 🗝/⚿ "Primary Key" glyph, shared with NodeKey
// (the same glyph a Keys-folder primary/unique key entry uses).
func primaryKeyIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '🗝'
	}
	return '⚿'
}

// folderIcon returns the container glyph for a style, open vs. closed.
func folderIcon(style config.IconStyle, expanded bool) rune {
	if style == config.IconStyleEmoji {
		if expanded {
			return '📂'
		}
		return '📁'
	}
	// Symbols and Portable share the same geometric folder glyphs.
	if expanded {
		return '▾'
	}
	return '▸'
}

// objectIcon returns the glyph for a concrete (non-container) node type.
func objectIcon(t NodeType, style config.IconStyle) rune {
	switch style {
	case config.IconStyleEmoji:
		return objectIconEmoji(t)
	case config.IconStylePortable:
		return objectIconPortable(t)
	default: // Symbols
		return objectIconSymbols(t)
	}
}

// objectIconEmoji and objectIconSymbols read nodeKinds' glyphs; '•' is the
// shared fallback for a type with none.
func objectIconEmoji(t NodeType) rune {
	if r := kindOf(t).emoji; r != 0 {
		return r
	}
	return '•'
}

func objectIconSymbols(t NodeType) rune {
	if r := kindOf(t).symbol; r != 0 {
		return r
	}
	return '•'
}

// objectIconPortable is the Symbols set with one substitution: a column (a
// table's or a view's) uses the plain '•' bullet, since '⁞' isn't guaranteed
// to render everywhere.
func objectIconPortable(t NodeType) rune {
	if t == NodeColumn || t == NodeViewColumn {
		return '•'
	}
	return objectIconSymbols(t)
}
