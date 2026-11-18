package format

import (
	"context"
	"fmt"
	"strings"
)

// verify is the runtime half of "formatting never changes meaning": it
// compares in and out as token streams with whitespace ignored, and any
// difference refuses the edit (ErrGuard) instead of handing the user a
// formatter bug. Tests can't cover every document a user formats; this runs
// on each one.
//
// A language whose formatter has no case here fails closed — registering a
// formatter without its guard can't ship output.
func verify(ctx context.Context, l Language, in, out string) error {
	return verifyLexed(ctx, l, in, nil, out)
}

// Verify is the guard Format runs, for a caller that formatted part of a
// document and must check the result in the whole one: a selection that starts
// inside a string, comment or CDATA section above it passes the guard on its
// own (it lexes as code) yet rewrites that value in place. It returns nil or
// an error wrapping ErrGuard, or ctx's error when ctx ends first.
func Verify(ctx context.Context, l Language, before, after string) error {
	return verify(ctx, l, before, after)
}

// verifyLexed is verify with in's SQL lexing already done (inLex, from
// lexSQL(in)); nil lexes in here.
//
// A lexing that stopped because ctx ended reads as text that "no longer
// tokenizes", so ctx's error is checked before any ErrGuard is reported: a
// cancelled format is not a formatter bug.
func verifyLexed(ctx context.Context, l Language, in string, inLex *sqlLexed, out string) error {
	err := verifyTokens(ctx, l, in, inLex, out)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
	}
	return err
}

// verifyTokens is verifyLexed's comparison, per language.
func verifyTokens(ctx context.Context, l Language, in string, inLex *sqlLexed, out string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch l {
	case LangJSON:
		a, b := jsonSignificant(in), jsonSignificant(out)
		if a == b {
			return nil
		}
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		return fmt.Errorf("%w (JSON differs after %d significant bytes)", ErrGuard, i)
	case LangXML:
		if strings.HasPrefix(in, bom) != strings.HasPrefix(out, bom) {
			return fmt.Errorf("%w (XML byte-order mark changed)", ErrGuard)
		}
		a, errA := xmlSignificant(ctx, in)
		b, errB := xmlSignificant(ctx, out)
		if errA != nil || errB != nil {
			return fmt.Errorf("%w (XML no longer tokenizes)", ErrGuard)
		}
		i := 0
		for i < len(a) && i < len(b) && a[i].kind == b[i].kind && in[a[i].start:a[i].end] == out[b[i].start:b[i].end] {
			i++
		}
		if i == len(a) && i == len(b) {
			return nil
		}
		return fmt.Errorf("%w (XML differs at token %d)", ErrGuard, i+1)
	case LangSQL:
		n, same, err := sqlSameTokens(ctx, in, inLex, out)
		if err != nil {
			return fmt.Errorf("%w (SQL no longer tokenizes)", ErrGuard)
		}
		if same {
			return nil
		}
		return fmt.Errorf("%w (SQL differs at token %d)", ErrGuard, n+1)
	}
	return fmt.Errorf("%w (no check for %s)", ErrGuard, l)
}

// jsonSignificant is s with the whitespace between JSON tokens removed;
// string contents (escapes included) are kept verbatim. Two texts that agree
// here are the same JSON token stream. It doesn't validate: the formatter has
// already parsed the input.
func jsonSignificant(s string) string {
	b := make([]byte, 0, len(s))
	inStr, esc := false, false
	for i := range len(s) {
		c := s[i]
		switch {
		case inStr:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == ' ', c == '\t', c == '\n', c == '\r':
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

// xmlSignificant is s's XML tokens — compared by kind and exact spelling —
// with the whitespace-only text between markup left out — except where it can be
// part of a value: inside an element (or at top level) that also holds other
// text or CDATA, and as an element's sole content (<a> </a>). It re-derives
// that from the token stream itself rather than reusing the formatter's tree.
func xmlSignificant(ctx context.Context, s string) ([]xmlTok, error) {
	toks, err := xmlTokens(ctx, s)
	if err != nil {
		return nil, err
	}
	type frame struct {
		ws      []int // whitespace-only text tokens directly inside
		textual bool  // other text or CDATA directly inside
		n       int   // tokens directly inside
	}
	keep := make([]bool, len(toks))
	stack := []frame{{}}
	settle := func(f frame) {
		if f.textual || f.n == 1 && len(f.ws) == 1 {
			for _, i := range f.ws {
				keep[i] = true
			}
		}
	}
	for i, t := range toks {
		top := &stack[len(stack)-1]
		keep[i] = true
		switch t.kind {
		case xmlStart:
			top.n++
			stack = append(stack, frame{})
			continue
		case xmlEnd:
			if len(stack) > 1 {
				settle(stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
			continue
		case xmlText:
			if strings.Trim(s[t.start:t.end], xmlSpace) == "" {
				top.ws = append(top.ws, i)
				keep[i] = false
			} else {
				top.textual = true
			}
		case xmlCDATA:
			top.textual = true
		}
		top.n++
	}
	for _, f := range stack {
		settle(f)
	}
	sig := toks[:0]
	for i, t := range toks {
		if keep[i] {
			sig = append(sig, t)
		}
	}
	return sig, nil
}
