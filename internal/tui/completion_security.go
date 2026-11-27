package tui

import (
	"slices"
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
)

// The vocabulary is documented by GRANT/DENY/REVOKE and
// sys.fn_builtin_permissions on Microsoft Learn. SERVER permissions have no ON.
var securityClasses = []string{
	"APPLICATION ROLE", "ASSEMBLY", "ASYMMETRIC KEY", "AVAILABILITY GROUP",
	"CERTIFICATE", "CONTRACT", "DATABASE", "DATABASE SCOPED CREDENTIAL",
	"ENDPOINT", "FULLTEXT CATALOG", "FULLTEXT STOPLIST", "LOGIN", "MESSAGE TYPE",
	"OBJECT", "REMOTE SERVICE BINDING", "ROLE", "ROUTE", "SCHEMA",
	"SEARCH PROPERTY LIST", "SERVER ROLE", "SERVICE", "SYMMETRIC KEY", "TYPE",
	"USER", "XML SCHEMA COLLECTION",
}

var securityObjectKinds = []gosmo.SecurableClass{
	gosmo.SecurableTable, gosmo.SecurableView, gosmo.SecurableProcedure,
	gosmo.SecurableScalarFunction, gosmo.SecurableInlineFunction,
	gosmo.SecurableTableFunction, gosmo.SecurableSequence, gosmo.SecurableSynonym,
}

const (
	securityQueueClass gosmo.SecurableClass = "QUEUE"
	securityTVFClass   gosmo.SecurableClass = "TVF"
)

type securityTarget struct {
	class string
	name  []sqltext.Token
}

func securityStatementTokens(pos *completionPosition) []sqltext.Token {
	if len(pos.tokens) == 0 {
		return nil
	}
	var tokens []sqltext.Token
	for at := pos.tokens[0].Start; at < len(pos.stmt); {
		tok := keywordNextToken(pos.stmt, at)
		if tok.Kind == sqltext.KindEnd {
			break
		}
		tokens = append(tokens, tok)
		at = tok.End
	}
	return tokens
}

func securityWord(buf []rune, tok sqltext.Token, word string) bool {
	return tok.Kind == sqltext.KindWord && strings.EqualFold(string(buf[tok.Start:tok.End]), word)
}

func securityPunct(buf []rune, tok sqltext.Token, punct string) bool {
	return tok.Kind == sqltext.KindPunct && string(buf[tok.Start:tok.End]) == punct
}

func securityWords(buf []rune, tokens []sqltext.Token) string {
	var words []string
	for _, tok := range tokens {
		if tok.Kind != sqltext.KindWord {
			return ""
		}
		words = append(words, strings.ToUpper(string(buf[tok.Start:tok.End])))
	}
	return strings.Join(words, " ")
}

func securityClauseAt(buf []rune, tokens []sqltext.Token, words ...string) int {
	depth := 0
	for i, tok := range tokens {
		switch {
		case securityPunct(buf, tok, "("):
			depth++
		case securityPunct(buf, tok, ")"):
			depth--
		case depth == 0:
			for _, word := range words {
				if securityWord(buf, tok, word) {
					return i
				}
			}
		}
	}
	return -1
}

func (p *QueryPanel) securityTargetAt(buf []rune, tokens []sqltext.Token) securityTarget {
	on := securityClauseAt(buf, tokens, "ON")
	if on < 0 || !p.securityPermissionHeader(buf, tokens[:on]) {
		return securityTarget{}
	}
	tail := tokens[on+1:]
	if end := securityClauseAt(buf, tail, "TO", "FROM"); end >= 0 {
		tail = tail[:end]
	}
	target := securityTarget{class: "OBJECT", name: tail}
	for i := 0; i+1 < len(tail); i++ {
		if securityPunct(buf, tail[i], ":") && securityPunct(buf, tail[i+1], ":") {
			target.class, target.name = securityWords(buf, tail[:i]), tail[i+2:]
			break
		}
	}
	for i, tok := range target.name {
		if securityPunct(buf, tok, "(") {
			target.name = target.name[:i]
			break
		}
	}
	return target
}

func securityPermissionNames(buf []rune, tokens []sqltext.Token) []string {
	var names []string
	start, depth := 0, 0
	for i, tok := range tokens {
		switch {
		case securityPunct(buf, tok, "("):
			if depth == 0 {
				names = append(names, securityWords(buf, tokens[start:i]))
			}

			depth++
		case securityPunct(buf, tok, ")"):
			depth--
			start = i + 1
		case depth == 0 && securityPunct(buf, tok, ","):
			if start < i {
				names = append(names, securityWords(buf, tokens[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(tokens) && depth == 0 {
		names = append(names, securityWords(buf, tokens[start:]))
	}
	return names
}

func (p *QueryPanel) securityPermissionHeader(buf []rune, tokens []sqltext.Token) bool {
	depth := 0
	for _, tok := range tokens {
		switch {
		case securityPunct(buf, tok, "("):
			depth++
		case securityPunct(buf, tok, ")"):
			depth--
		case securityPunct(buf, tok, ","):
		case tok.Kind == sqltext.KindQuotedIdent && depth > 0:
		case tok.Kind == sqltext.KindWord:
			if depth == 0 && slices.Contains([]string{"FROM", "JOIN", "WHERE", "SET", "INTO", "VALUES", "DENY", "REVOKE"},
				strings.ToUpper(string(buf[tok.Start:tok.End]))) {
				return false
			}
		default:
			return false
		}
	}
	if depth != 0 {
		return false
	}
	if len(tokens) >= 3 && securityWords(buf, tokens[:3]) == "GRANT OPTION FOR" {
		tokens = tokens[3:]
	}
	defs := p.securityPermissionDefinitions()
	return !slices.ContainsFunc(securityPermissionNames(buf, tokens), func(name string) bool {
		return name != "ALL" && name != "ALL PRIVILEGES" && !slices.ContainsFunc(defs, func(def gosmo.BuiltinPermission) bool {
			return strings.HasPrefix(def.Name, name)
		})
	})
}

func (p *QueryPanel) securityClassSupported(class string) bool {
	if p.conn.IsFabricDW() {
		return slices.Contains([]string{"DATABASE", "OBJECT", "SCHEMA", "ROLE", "USER"}, class)
	}
	if p.conn.Server != nil && p.conn.Server.Info().IsDatabaseScoped() &&
		slices.Contains([]string{"SERVER", "LOGIN", "SERVER ROLE", "ENDPOINT", "AVAILABILITY GROUP",
			"CONTRACT", "MESSAGE TYPE", "REMOTE SERVICE BINDING", "ROUTE", "SERVICE"}, class) {
		return false
	}
	return true
}

func (p *QueryPanel) securityCompletionCandidates(pos *completionPosition, post *completionPost) ([]editor.CompletionItem, bool) {
	all := securityStatementTokens(pos)
	if len(all) == 0 || all[0].Start >= pos.at {
		return nil, false
	}
	buf := pos.stmt
	verb := securityWords(buf, all[:1])
	if !slices.Contains([]string{"GRANT", "DENY", "REVOKE"}, verb) {
		return nil, false
	}
	post.collation = p.securityCollation(false)
	var head []sqltext.Token
	for _, tok := range all[1:] {
		if tok.Start >= pos.at {
			break
		}
		head = append(head, tok)
	}
	grantOnly := false
	if verb == "REVOKE" && len(head) > 0 && securityWord(buf, head[0], "GRANT") {
		option := strings.Fields("GRANT OPTION FOR")
		for i := 0; i < min(len(head), len(option)); i++ {
			if !securityWord(buf, head[i], option[i]) {
				return nil, true
			}
		}
		if len(head) < len(option) {
			return securityKeywordItems(pos, []string{strings.Join(option[len(head):], " ")}), true
		}
		grantOnly, head = true, head[3:]
	}
	target := p.securityTargetAt(buf, all[1:])
	if !p.securityClassSupported(target.class) && target.class != "" {
		return nil, true
	}
	on, recipient := securityClauseAt(buf, head, "ON"), securityClauseAt(buf, head, "TO", "FROM")
	permissionsEnd := len(head)
	if on >= 0 {
		permissionsEnd = on
	} else if recipient >= 0 {
		permissionsEnd = recipient
	}
	permissionTokens := head[:permissionsEnd]
	permissions := securityPermissionNames(buf, permissionTokens)
	if recipient >= 0 {
		server := target.class == "LOGIN" || target.class == "SERVER ROLE" ||
			target.class == "ENDPOINT" || target.class == "AVAILABILITY GROUP" ||
			target.class == "" && p.securityServerPermissions(permissions)
		if server && !p.securityClassSupported("SERVER") {
			return nil, true
		}
		post.collation = p.securityCollation(server)
		return p.securityRecipientCandidates(pos, head[recipient+1:], verb, server), true
	}
	depth, open := 0, -1
	for i, tok := range head {
		if securityPunct(buf, tok, "(") {
			depth++
			open = i
		} else if securityPunct(buf, tok, ")") {
			depth--
		}
	}
	if depth > 0 {
		if on < 0 && len(permissions) > 0 {
			permissions = permissions[len(permissions)-1:]
		}
		return p.securityColumnCandidates(pos, target, permissions, head[open+1:]), true
	}
	if on >= 0 {
		return p.securityTargetCandidates(pos, head[on+1:], target, permissions, verb), true
	}
	return p.securityPermissionCandidates(pos, permissionTokens, target, verb, grantOnly), true
}

func securityKeywordItems(pos *completionPosition, choices []string) []editor.CompletionItem {
	if pos.state != sqlparse.LexNormal || pos.hasQualifier || pos.memberAccess {
		return nil
	}
	var items []editor.CompletionItem
	for _, choice := range choices {
		if !strings.HasPrefix(choice, strings.ToUpper(pos.prefix)) {
			continue
		}
		text, _ := completionPhrasePrefix(pos.stmt, pos.upTo, choice)
		items = append(items, editor.CompletionItem{Text: text, Label: choice, Detail: "keyword",
			AppendSpace: true, Soft: pos.prefix == ""})
	}
	return items
}

func (p *QueryPanel) securityPermissionCandidates(pos *completionPosition, tokens []sqltext.Token, target securityTarget, verb string, grantOnly bool) []editor.CompletionItem {
	start := 0
	for i, tok := range tokens {
		if securityPunct(pos.stmt, tok, ",") || securityPunct(pos.stmt, tok, ")") {
			start = i + 1
		}
	}
	current := securityWords(pos.stmt, tokens[start:])
	definitions := p.securityPermissionDefinitions()
	objectClass := p.securityObjectClass(pos.stmt, target)
	var choices []string
	used := securityPermissionNames(pos.stmt, tokens[:start])
	complete := false
	for _, def := range definitions {
		if !p.securityClassSupported(def.Class) || target.class != "" && def.Class != target.class ||
			!p.securityClassAllows(def.Class, used) ||
			def.Class == "OBJECT" && !p.securityObjectAllows(objectClass, def.Name) ||
			slices.Contains(used, def.Name) {
			continue
		}
		if def.Name == current {
			complete = true
		}
		suffix := def.Name
		if current != "" {
			var ok bool
			suffix, ok = strings.CutPrefix(suffix, current+" ")
			if !ok {
				continue
			}
		}
		choices = append(choices, suffix)
	}
	if !p.conn.IsFabricDW() && start == 0 && current == "" {
		choices = append(choices, "ALL")
		if verb == "REVOKE" && !grantOnly {
			choices = append(choices, "GRANT OPTION FOR")
		}
	}
	if current == "ALL" && !p.conn.IsFabricDW() {
		choices, complete = append(choices, "PRIVILEGES"), true
	}
	if current == "ALL PRIVILEGES" && !p.conn.IsFabricDW() {
		complete = true
	}
	afterColumns := start > 0 && len(tokens[start:]) == 0 && securityPunct(pos.stmt, tokens[start-1], ")")
	if afterColumns {
		choices, complete = nil, true
	}
	if complete {
		names := securityPermissionNames(pos.stmt, tokens)
		if !p.securityServerPermissions(names) || !p.securityClassAllows("SERVER", names) {
			choices = append(choices, "ON")
		}
		if !afterColumns && (current == "ALL" || current == "ALL PRIVILEGES" || slices.ContainsFunc(definitions, func(def gosmo.BuiltinPermission) bool {
			return def.Name == current && (def.Class == "DATABASE" || def.Class == "SERVER") && p.securityClassSupported(def.Class)
		})) {
			choices = append(choices, "TO")
			if verb == "REVOKE" {
				choices = append(choices, "FROM")
			}
		}
	}
	slices.Sort(choices)
	return securityKeywordItems(pos, slices.Compact(choices))
}

func (p *QueryPanel) securityRecipientCandidates(pos *completionPosition, tokens []sqltext.Token, verb string, server bool) []editor.CompletionItem {
	start := 0
	optionSeen := false
	for i, tok := range tokens {
		if i > start && tok.Kind == sqltext.KindWord &&
			slices.Contains([]string{"WITH", "CASCADE", "AS"}, strings.ToUpper(string(pos.stmt[tok.Start:tok.End]))) {
			optionSeen = true
		}
		if securityPunct(pos.stmt, tok, ",") {
			if optionSeen {
				return nil
			}
			start = i + 1
		}
	}
	tail := tokens[start:]
	if len(tail) == 0 {
		kind := "database-principals"
		if server {
			kind = "server-principals"
		}
		names, loading := p.securityNames(kind)
		return p.securityNameCandidates(pos, names, nil, tokens[:start], loading, p.securityCollation(server))
	}
	if tail[0].Kind != sqltext.KindWord && tail[0].Kind != sqltext.KindQuotedIdent {
		return nil
	}
	options := securityWords(pos.stmt, tail[1:])
	var choices []string
	if verb == "GRANT" {
		switch options {
		case "":
			choices = []string{"WITH GRANT OPTION"}
		case "WITH":
			choices = []string{"GRANT OPTION"}
		case "WITH GRANT":
			choices = []string{"OPTION"}
		}
	} else if options == "" && (verb == "DENY" || verb == "REVOKE") {
		choices = []string{"CASCADE"}
	}
	if !p.conn.IsFabricDW() && (options == "" || options == "WITH GRANT OPTION" || options == "CASCADE") {
		choices = append(choices, "AS")
	}
	as := securityClauseAt(pos.stmt, tail, "AS")
	if as >= 0 {
		if len(tail[as+1:]) != 0 {
			return nil
		}
		kind := "database-principals"
		if server {
			kind = "server-logins"
		}
		names, loading := p.securityNames(kind)
		return p.securityNameCandidates(pos, names, nil, nil, loading, p.securityCollation(server))
	}
	return securityKeywordItems(pos, choices)
}

func securityNameParts(buf []rune, tokens []sqltext.Token) ([]string, bool) {
	var parts []string
	expectName := true
	for _, tok := range tokens {
		if expectName && (tok.Kind == sqltext.KindWord || tok.Kind == sqltext.KindQuotedIdent) {
			parts = append(parts, gosmo.UnquoteName(string(buf[tok.Start:tok.End])))
			expectName = false
		} else if !expectName && securityPunct(buf, tok, ".") {
			expectName = true
		} else {
			return nil, false
		}
	}
	return parts, expectName
}

func (p *QueryPanel) securityTargetCandidates(pos *completionPosition, tokens []sqltext.Token, target securityTarget, permissions []string, verb string) []editor.CompletionItem {
	var choices []string
	colons := -1
	for i := 0; i+1 < len(tokens); i++ {
		if securityPunct(pos.stmt, tokens[i], ":") && securityPunct(pos.stmt, tokens[i+1], ":") {
			colons = i
			break
		}
	}
	nameTokens := tokens
	if colons >= 0 {
		nameTokens = tokens[colons+2:]
	} else {
		current := securityWords(pos.stmt, tokens)
		for _, class := range securityClasses {
			if !p.securityClassSupported(class) || !p.securityClassAllows(class, permissions) {
				continue
			}
			suffix := class
			if current != "" {
				var ok bool
				suffix, ok = strings.CutPrefix(class, current+" ")
				if !ok {
					continue
				}
			}
			choices = append(choices, suffix+"::")
		}
		if current != "" && slices.Contains(securityClasses, current) {
			choices = append(choices, "::")
		}
	}
	parts, incomplete := securityNameParts(pos.stmt, nameTokens)
	if len(nameTokens) != 0 && !incomplete {
		if securityWords(pos.stmt, nameTokens) == "" {
			choices = nil
		}
		if pos.prefix == "" || !pos.hasQualifier {
			choices = append(choices, "TO")
			if verb == "REVOKE" {
				choices = append(choices, "FROM")
			}
		}
		return securityKeywordItems(pos, choices)
	}
	if !p.securityClassAllows(target.class, permissions) {
		return securityKeywordItems(pos, choices)
	}
	database := p.database
	inv := p.app.ensureCompletionInventory(p.conn, database)
	if len(parts) > 1 {
		if target.class != "OBJECT" || !p.conn.IsFabricDW() || len(parts) != 2 ||
			!gosmo.SameName(serverCollation(p.conn), parts[0], database) {
			return nil
		}
		parts = parts[1:]
	}
	names, loading := p.securityNamesInDatabase(database, target.class)
	if target.class == "OBJECT" {
		names = slices.DeleteFunc(slices.Clone(names), func(name securityName) bool {
			return slices.ContainsFunc(permissions, func(permission string) bool {
				return !p.securityObjectAllows(name.objectClass, permission)
			})
		})
	}
	items := p.securityNameCandidates(pos, names, parts, nil, loading, inv.collation)
	if len(parts) == 0 && slices.Contains([]string{"OBJECT", "TYPE", "XML SCHEMA COLLECTION"}, target.class) {
		if inv.catalog != nil {
			items = append(items, p.databaseSchemaItems(inv, nil, pos.prefix)...)
		}
	}
	return append(securityKeywordItems(pos, choices), items...)
}

func (p *QueryPanel) securityColumnCandidates(pos *completionPosition, target securityTarget, permissions []string, tokens []sqltext.Token) []editor.CompletionItem {
	if target.class != "OBJECT" {
		return nil
	}
	class := p.securityObjectClass(pos.stmt, target)
	if len(permissions) == 0 || class != "" && class != securityTVFClass && !class.HasColumns() ||
		slices.ContainsFunc(permissions, func(permission string) bool {
			return !slices.Contains([]string{"SELECT", "REFERENCES", "UPDATE", "UNMASK"}, permission) ||
				!p.securityObjectAllows(class, permission)
		}) {
		return nil
	}
	parts, _ := securityNameParts(pos.stmt, target.name)
	if len(parts) == 0 || len(parts) > 3 {
		return nil
	}
	inv := p.app.ensureCompletionInventory(p.conn, p.database)
	if len(parts) == 3 {
		if !p.conn.IsFabricDW() || !gosmo.SameName(serverCollation(p.conn), parts[0], p.database) {
			return nil
		}
		parts = parts[1:]
	}
	if inv.loading {
		return []editor.CompletionItem{loadingCompletionItem}
	}

	if inv.err != nil || inv.catalog == nil {
		return nil
	}
	name, schema := parts[len(parts)-1], ""
	if len(parts) == 2 {
		schema = parts[0]
	}
	used := newNameSet(inv.collation)
	for _, tok := range tokens {
		if tok.Kind == sqltext.KindWord || tok.Kind == sqltext.KindQuotedIdent {
			used.Add(gosmo.UnquoteName(string(pos.stmt[tok.Start:tok.End])))
		}
	}
	var items []editor.CompletionItem
	for _, schema := range inv.qualifierSchemas(schema) {
		obj := findCatalogObject(inv, nil, schema, name)
		if obj == nil {
			obj, _ = inv.fnByQualifiedName.Get(qualifiedKey(schema, name))
		}
		if obj == nil {
			continue
		}
		for _, col := range obj.Columns {
			if used.Has(col.Name) {
				continue
			}
			if ok, partial := nameMatch(col.Name, strings.ToLower(pos.prefix)); ok {
				items = append(items, editor.CompletionItem{Text: gosmo.QuoteNameIfNeeded(col.Name),
					Label: col.Name, Detail: "column", Partial: partial})
			}
		}
		break
	}
	sortCompletionItems(items)
	return items
}

func (p *QueryPanel) securityClassAllows(class string, permissions []string) bool {
	defs := p.securityPermissionDefinitions()
	return !slices.ContainsFunc(permissions, func(permission string) bool {
		if permission == "ALL" || permission == "ALL PRIVILEGES" {
			return class != "DATABASE" && class != "OBJECT"
		}
		return !slices.ContainsFunc(defs, func(def gosmo.BuiltinPermission) bool {
			return def.Class == class && def.Name == permission
		})
	})
}

func (p *QueryPanel) securityObjectAllows(class gosmo.SecurableClass, permission string) bool {
	if class == "" {
		return true
	}
	if permission == "ALL" || permission == "ALL PRIVILEGES" {
		return slices.Contains([]gosmo.SecurableClass{gosmo.SecurableTable, gosmo.SecurableView, gosmo.SecurableProcedure,
			gosmo.SecurableScalarFunction, gosmo.SecurableInlineFunction, gosmo.SecurableTableFunction, securityTVFClass}, class)
	}
	if permission == "RECEIVE" {
		return class == securityQueueClass
	}
	if class == securityQueueClass {
		return slices.Contains([]string{"ALTER", "CONTROL", "REFERENCES", "TAKE OWNERSHIP", "VIEW DEFINITION",
			"VIEW SECURITY DEFINITION", "VIEW PERFORMANCE DEFINITION"}, permission)
	}
	if class == securityTVFClass {
		return slices.Contains(gosmo.SecurableInlineFunction.PermissionNames(), permission) ||
			slices.Contains(gosmo.SecurableTableFunction.PermissionNames(), permission)
	}
	if permission == "UNMASK" {
		return class.HasColumns() && slices.ContainsFunc(p.securityPermissionDefinitions(), func(def gosmo.BuiltinPermission) bool {
			return def.Class == "OBJECT" && def.Name == "UNMASK"
		})
	}
	if slices.ContainsFunc(securityObjectKinds, func(kind gosmo.SecurableClass) bool {
		return slices.Contains(kind.PermissionNames(), permission)
	}) {
		return slices.Contains(class.PermissionNames(), permission)
	}
	return true // newer engine vocabulary is not limited to the library's write allowlist
}

func (p *QueryPanel) securityObjectClass(buf []rune, target securityTarget) gosmo.SecurableClass {
	if target.class != "OBJECT" {
		return ""
	}
	parts, _ := securityNameParts(buf, target.name)
	if len(parts) == 0 || len(parts) > 3 {
		return ""
	}
	database := p.database
	inv := p.app.completion.inventories[completionInventoryKey(p.conn.Opts, database)]
	if len(parts) == 3 {
		if !p.conn.IsFabricDW() || !gosmo.SameName(serverCollation(p.conn), parts[0], database) {
			return ""
		}
		parts = parts[1:]
	}
	name, schema := parts[len(parts)-1], ""
	if len(parts) == 2 {
		schema = parts[0]
	}
	if inv != nil && inv.catalog != nil {
		for _, schema := range inv.qualifierSchemas(schema) {
			if obj := findCatalogObject(inv, nil, schema, name); obj != nil {
				switch obj.Type {
				case gosmo.CatalogTable:
					return gosmo.SecurableTable
				case gosmo.CatalogView:
					return gosmo.SecurableView
				}
			}
			for _, proc := range inv.procedures {
				if inv.sameName(proc.Name, name) && inv.sameName(proc.Schema, schema) {
					return gosmo.SecurableProcedure
				}
			}
		}
	}
	cached := p.app.completion.security[securityInventoryKey(p.conn, database, "OBJECT")]
	if cached != nil {
		for _, x := range cached.names {
			if gosmo.SameName(p.securityCollation(false), x.name, name) &&
				(schema == "" || gosmo.SameName(p.securityCollation(false), x.schema, schema)) {
				return x.objectClass
			}
		}
	}
	if inv != nil {
		for _, schema := range inv.qualifierSchemas(schema) {
			if _, ok := inv.fnByQualifiedName.Get(qualifiedKey(schema, name)); ok {
				return securityTVFClass
			}
		}
	}
	return ""
}
