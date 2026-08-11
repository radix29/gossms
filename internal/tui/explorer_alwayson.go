package tui

import (
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// explorer_alwayson.go holds the loaders for the "Always On High Availability"
// branch: Availability Groups, and under each group its Replicas, Databases
// and Listeners.
//
// # Reading from the primary
//
// Only sys.availability_groups and sys.availability_replicas are cluster-wide.
// The sys.dm_hadr_* DMVs describe what the connected instance can see, so a
// secondary reports empty roles, empty health and no per-database queue detail
// for every replica but itself — a tree built from a secondary would show most
// of the group blank without saying why. Every loader here therefore resolves
// the group's primary and re-reads through db.ServerConn.Peer, falling back to
// the local (partial) view when the primary is unreachable rather than failing
// the expansion outright. agView carries which of the two happened so labels
// can say so.

// loadAlwaysOnChildren returns the Always On root's children. Always On being
// disabled is reported as a node rather than an error: it is a normal state
// for most instances, and SSMS shows the folder regardless.
func loadAlwaysOnChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	if info := l.sc.Server.Info(); info != nil && !info.IsHADREnabled {
		return []*explorerNode{
			l.node("Always On is not enabled on this instance", NodeError, "", "", ""),
		}, nil
	}
	return []*explorerNode{
		l.node("Availability Groups", NodeAvailabilityGroups, "", "", ""),
	}, nil
}

// loadAvailabilityGroupsChildren lists the groups this instance participates
// in, labelling each with the local replica's role the way SSMS does.
func loadAvailabilityGroupsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(
		func() ([]*gosmo.AvailabilityGroup, error) {
			return l.sc.Server.AvailabilityGroupsContext(l.ctx)
		},
		func(ag *gosmo.AvailabilityGroup) *explorerNode {
			n := l.node(agLabel(ag.Name, ag.PrimaryReplicaServerName, ag.IsLocalPrimary()),
				NodeAvailabilityGroup, "", ag.Name, "")
			n.data.AGName = ag.Name
			return n
		})
}

// agLabel renders a group as "AAG1 (Primary)" — the local replica's role,
// matching SSMS. A group whose primary this instance cannot see is marked so
// explicitly rather than silently reading as a secondary: an empty
// primaryReplica means "unknown from here", which is what a resolving or
// disconnected group looks like, and calling that "Secondary" would claim a
// healthy group where there isn't one.
//
// Takes the three values rather than the group because IsLocalPrimary depends
// on gosmo's unexported server back-pointer, which no test outside gosmo can
// populate.
func agLabel(name, primaryReplica string, isLocalPrimary bool) string {
	switch {
	case primaryReplica == "":
		return name + " (Not synchronizing)"
	case isLocalPrimary:
		return name + " (Primary)"
	default:
		return name + " (Secondary)"
	}
}

// loadAvailabilityGroupChildren returns one group's three folders.
func loadAvailabilityGroupChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	ag := node.data.Name
	folders := []struct {
		label string
		typ   NodeType
	}{
		{"Availability Replicas", NodeAvailabilityReplicas},
		{"Availability Databases", NodeAvailabilityDatabases},
		{"Availability Group Listeners", NodeAGListeners},
	}
	out := make([]*explorerNode, 0, len(folders))
	for _, f := range folders {
		n := l.node(f.label, f.typ, "", ag, "")
		n.data.AGName = ag
		out = append(out, n)
	}
	return out, nil
}

// agView is one availability group resolved for reading, together with the
// connection it should be read through.
type agView struct {
	ag *gosmo.AvailabilityGroup

	// followed is true when sc is a peer connection to the primary rather
	// than the tree's own connection.
	followed bool

	// unreachable names the primary that could not be connected to, if
	// following was attempted and failed. Empty otherwise.
	unreachable string
}

// resolveAGView looks up a group by name and, when the local instance is not
// its primary, re-reads it through a connection to the primary.
//
// A failure to reach the primary is deliberately not an error: the local view
// is incomplete but still worth showing, and an unreachable peer (different
// port, firewalled, different credentials) must not make the whole branch
// unexpandable. Callers surface view.unreachable instead.
func resolveAGView(l loaderCtx, name string) (agView, error) {
	ag, err := l.sc.Server.AvailabilityGroupByNameContext(l.ctx, name)
	if err != nil {
		return agView{}, err
	}
	if ag.IsLocalPrimary() || ag.PrimaryReplicaServerName == "" {
		return agView{ag: ag}, nil
	}

	primary := ag.PrimaryReplicaServerName
	peer, err := l.sc.Peer(l.ctx, primary)
	if err != nil {
		return agView{ag: ag, unreachable: primary}, nil
	}
	primaryAG, err := peer.Server.AvailabilityGroupByNameContext(l.ctx, name)
	if err != nil {
		return agView{ag: ag, unreachable: primary}, nil
	}
	return agView{ag: primaryAG, followed: true}, nil
}

// followNote is the trailing node explaining where the data came from, or nil
// when it came from the primary the user is already connected to. Without it a
// half-empty replica list from a secondary looks like a fault.
func (v agView) followNote(l loaderCtx) *explorerNode {
	switch {
	case v.unreachable != "":
		return l.node(fmt.Sprintf("(partial — primary %s unreachable)", v.unreachable), NodeError, "", "", "")
	case v.followed:
		return l.node(fmt.Sprintf("(read from primary %s)", v.ag.PrimaryReplicaServerName), NodeLoading, "", "", "")
	}
	return nil
}

// appendNote adds v's provenance note to children, if there is one.
func (v agView) appendNote(l loaderCtx, children []*explorerNode) []*explorerNode {
	if n := v.followNote(l); n != nil {
		return append(children, n)
	}
	return children
}

func loadAvailabilityReplicasChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	view, err := resolveAGView(l, node.data.AGName)
	if err != nil {
		return nil, err
	}
	replicas, err := view.ag.ReplicasContext(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(replicas)+1)
	for _, r := range replicas {
		n := l.node(replicaLabel(r), NodeAvailabilityReplica, "", r.ReplicaServerName, "")
		n.data.AGName = node.data.AGName
		n.data.AGIsPrimary = strings.EqualFold(r.Role, "PRIMARY")
		out = append(out, n)
	}
	return view.appendNote(l, out), nil
}

// replicaLabel renders "ubusql2 (Secondary, Synchronous commit)". The role is
// omitted when the DMV has no state row for the replica, which is what a
// secondary sees for its peers.
func replicaLabel(r *gosmo.AvailabilityReplica) string {
	parts := make([]string, 0, 2)
	if r.Role != "" {
		parts = append(parts, titleWord(r.Role))
	}
	if r.AvailabilityMode != "" {
		parts = append(parts, commitModeName(r.AvailabilityMode))
	}
	if len(parts) == 0 {
		return r.ReplicaServerName
	}
	return r.ReplicaServerName + " (" + strings.Join(parts, ", ") + ")"
}

// commitModeName turns SYNCHRONOUS_COMMIT into "Synchronous commit", the
// wording SSMS's replica grid uses.
func commitModeName(mode string) string {
	switch strings.ToUpper(mode) {
	case "SYNCHRONOUS_COMMIT":
		return "Synchronous commit"
	case "ASYNCHRONOUS_COMMIT":
		return "Asynchronous commit"
	case "CONFIGURATION_ONLY":
		return "Configuration only"
	}
	return titleWord(mode)
}

// titleWord renders an all-caps SQL Server enum ("SECONDARY", "NOT
// SYNCHRONIZING") as "Secondary" / "Not synchronizing".
func titleWord(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "_", " ")
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func loadAvailabilityDatabasesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	view, err := resolveAGView(l, node.data.AGName)
	if err != nil {
		return nil, err
	}
	dbs, err := view.ag.DatabasesContext(l.ctx)
	if err != nil {
		return nil, err
	}

	localSecondary, joined := agLocalDatabaseJoinState(l, view, dbs, node.data.AGName)

	summaries := summarizeAGDatabases(dbs)
	out := make([]*explorerNode, 0, len(summaries)+1)
	for _, s := range summaries {
		n := l.node(agDatabaseLabel(s.Name, s.States, s.Suspended, s.Unhealthy), NodeAvailabilityDatabase, "", s.Name, s.Name)
		n.data.AGName = node.data.AGName
		n.data.AGSuspended = s.Suspended
		n.data.AGLocalSecondary = localSecondary
		n.data.AGLocalJoined = joined[strings.ToLower(s.Name)]
		out = append(out, n)
	}
	return view.appendNote(l, out), nil
}

// agLocalDatabaseJoinState reports whether the instance the tree is connected
// to is a secondary for this group, and which of the group's databases its own
// copy has joined.
//
// Read from the local connection even when the folder itself was read from the
// primary: joining is an ALTER DATABASE against one copy, so what matters is
// what *this* instance holds. A database is in the group cluster-wide from the
// moment ADD DATABASE runs on the primary, and appears here regardless — the
// local copy having no row in sys.dm_hadr_database_replica_states, and so no
// synchronization state, is exactly what "restored but not joined yet" looks
// like.
//
// Both return values are the safe-and-silent ones on any failure: a nil map
// gates Join and Unjoin off, which is what an unreadable group should do.
func agLocalDatabaseJoinState(l loaderCtx, view agView, dbs []*gosmo.AvailabilityDatabase, agName string) (localSecondary bool, joined map[string]bool) {
	local := dbs
	if view.followed {
		// dbs came from the primary's connection, whose rows describe the
		// primary's view; re-read from this instance.
		ag, err := l.sc.Server.AvailabilityGroupByNameContext(l.ctx, agName)
		if err != nil {
			return false, nil
		}
		if ag.IsLocalPrimary() {
			return false, nil
		}
		if local, err = ag.DatabasesContext(l.ctx); err != nil {
			return false, nil
		}
	} else if view.ag.IsLocalPrimary() {
		return false, nil
	}

	return true, agJoinedCopies(local, l.sc.Server.Name())
}

// agJoinedCopies maps a lower-cased database name to whether localName's own
// copy has joined the group.
//
// The test is having a synchronization state at all. AvailabilityGroup.Databases
// cross-joins the cluster-wide database list with the replica list, so a
// database the local copy has not joined still produces a row — with every
// sys.dm_hadr_database_replica_states column empty, because that DMV has no row
// for it until the join. An empty state is therefore "not joined here", not
// "unknown".
func agJoinedCopies(dbs []*gosmo.AvailabilityDatabase, localName string) map[string]bool {
	joined := map[string]bool{}
	for _, d := range dbs {
		if strings.EqualFold(d.ReplicaServerName, localName) {
			joined[strings.ToLower(d.DatabaseName)] = d.SynchronizationState != ""
		}
	}
	return joined
}

// agDatabaseSummary is one database of an availability group, collapsed across
// every replica that holds it.
type agDatabaseSummary struct {
	Name string

	// States is every distinct synchronization state the database is in across
	// replicas — more than one means the replicas disagree, which is the case
	// worth seeing rather than averaging away.
	States    []string
	Suspended bool
	Unhealthy bool
}

// summarizeAGDatabases collapses AvailabilityGroup.Databases' one row per
// (database, replica) into one entry per database, preserving the query's
// ordering: the first row seen fixes a database's position.
func summarizeAGDatabases(dbs []*gosmo.AvailabilityDatabase) []agDatabaseSummary {
	var out []agDatabaseSummary
	byName := map[string]int{}
	for _, d := range dbs {
		i, ok := byName[d.DatabaseName]
		if !ok {
			i = len(out)
			byName[d.DatabaseName] = i
			out = append(out, agDatabaseSummary{Name: d.DatabaseName})
		}
		s := &out[i]
		if d.IsSuspended {
			s.Suspended = true
		}
		if d.SynchronizationHealth != "" && !strings.EqualFold(d.SynchronizationHealth, "HEALTHY") {
			s.Unhealthy = true
		}
		if d.SynchronizationState != "" && !slicesContains(s.States, d.SynchronizationState) {
			s.States = append(s.States, d.SynchronizationState)
		}
	}
	return out
}

// agDatabaseLabel renders "testdb_1 (Synchronized)". Multiple distinct states
// across replicas are all shown — "(Synchronized, Synchronizing)" — because
// collapsing them to one would hide exactly the replica that is behind.
func agDatabaseLabel(name string, states []string, suspended, unhealthy bool) string {
	parts := make([]string, 0, len(states)+1)
	for _, s := range states {
		parts = append(parts, titleWord(s))
	}
	if suspended {
		parts = append(parts, "Suspended")
	}
	if unhealthy && !suspended {
		parts = append(parts, "Not healthy")
	}
	if len(parts) == 0 {
		return name
	}
	return name + " (" + strings.Join(parts, ", ") + ")"
}

func slicesContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// agLocalDatabaseStates maps a lower-cased database name to the availability
// state of the copy *this* instance holds, for the Databases folder's labels.
//
// Local-only on purpose. The Availability Databases folder summarises a
// database across every replica, because that folder is about the group; the
// Databases folder lists this instance's own databases, so annotating one with
// a peer's state would report something that is not true of the database being
// listed. SSMS makes the same distinction.
//
// Returns nil rather than an error at every failure: this decorates a list
// that has to render with or without Always On, so a disabled instance, a
// group mid-failover, and a failed query all just leave the labels bare.
func agLocalDatabaseStates(l loaderCtx) map[string]agDatabaseSummary {
	if info := l.sc.Server.Info(); info == nil || !info.IsHADREnabled {
		return nil
	}
	groups, err := l.sc.Server.AvailabilityGroupsContext(l.ctx)
	if err != nil || len(groups) == 0 {
		return nil
	}
	out := map[string]agDatabaseSummary{}
	for _, ag := range groups {
		dbs, err := ag.DatabasesContext(l.ctx)
		if err != nil {
			continue
		}
		local := make([]*gosmo.AvailabilityDatabase, 0, len(dbs))
		for _, d := range dbs {
			if d.IsLocal {
				local = append(local, d)
			}
		}
		for _, s := range summarizeAGDatabases(local) {
			out[strings.ToLower(s.Name)] = s
		}
	}
	return out
}

// agLabelForDatabase renders a Databases-folder label: the plain name, or the
// availability-group form when states has an entry for it. Falling back to the
// bare name is what keeps this safe on an instance with no Always On, where
// states is nil.
func agLabelForDatabase(name string, states map[string]agDatabaseSummary) string {
	s, ok := states[strings.ToLower(name)]
	if !ok {
		return name
	}
	return agDatabaseLabel(name, s.States, s.Suspended, s.Unhealthy)
}

func loadAGListenersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	view, err := resolveAGView(l, node.data.AGName)
	if err != nil {
		return nil, err
	}
	listeners, err := view.ag.ListenersContext(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(listeners)+1)
	for _, li := range listeners {
		n := l.node(listenerLabel(li), NodeAGListener, "", li.DNSName, "")
		n.data.AGName = node.data.AGName
		out = append(out, n)
	}
	return view.appendNote(l, out), nil
}

// listenerLabel renders "ubuaag (192.168.178.99, port 1433)".
func listenerLabel(li *gosmo.AvailabilityGroupListener) string {
	ips := make([]string, 0, len(li.IPAddresses))
	for _, ip := range li.IPAddresses {
		if ip.IsDHCP {
			ips = append(ips, "DHCP")
			continue
		}
		if ip.IPAddress != "" {
			ips = append(ips, ip.IPAddress)
		}
	}
	detail := fmt.Sprintf("port %d", li.Port)
	if len(ips) > 0 {
		detail = strings.Join(ips, ", ") + ", " + detail
	}
	return fmt.Sprintf("%s (%s)", li.DNSName, detail)
}

// alwaysOnRootLabel is the Always On node's label — the literal string
// loadServerChildren inserts and any future label refresh must match.
const alwaysOnRootLabel = "Always On High Availability"
