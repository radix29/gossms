// Package scripting is Script As without a screen: for each object family,
// the verbs it can be scripted at and how each verb's text is generated
// through gosmo's Scripter. Object Explorer's "Script <Noun> as ▸" cascade
// (internal/tui/scripting.go) and the command line's script commands both
// read this one table, so the two produce the same script for the same object
// by construction.
//
// What stays with each caller is presentation: the TUI's menu labels, nouns,
// permission gating and destinations; the command line's verb names and
// output. Headless: gosmo and the standard library only, never tcell or tui.
package scripting
