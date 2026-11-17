package main

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func newLabelTestStore(t *testing.T) *MessageStore {
	t.Helper()
	ms := newTestMessageStore(t)
	ms.db.SetMaxOpenConns(1)
	if _, err := ms.db.Exec(labelSchema); err != nil {
		t.Fatalf("label schema: %v", err)
	}
	return ms
}

func countRows(t *testing.T, ms *MessageStore, q string, args ...any) int {
	t.Helper()
	var n int
	if err := ms.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func TestLabelEventsPersistAndNormaliseLID(t *testing.T) {
	ms := newLabelTestStore(t)
	lid := types.NewJID("111", types.HiddenUserServer)
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	client := newTestClient(&mockLIDStore{pnByLID: map[types.JID]types.JID{lid: pn}})
	log := testLogger()
	now := time.Now()

	handleLabelEvent(client, ms, &events.LabelEdit{LabelID: "3", Timestamp: now, Action: &waSyncAction.LabelEditAction{
		Name: proto.String("Leads"), Color: proto.Int32(4), Type: waSyncAction.LabelEditAction_CUSTOM.Enum(),
	}}, log)
	handleLabelEvent(client, ms, &events.LabelAssociationChat{LabelID: "3", JID: lid, Timestamp: now,
		Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)}}, log)

	l, err := ms.GetLabel("3")
	if err != nil || l.Name != "Leads" || l.Color != 4 || l.ListType != "CUSTOM" {
		t.Fatalf("label = %+v, err %v", l, err)
	}
	var chat, target string
	if err := ms.db.QueryRow(`SELECT chat_jid, target_jid FROM chat_labels WHERE label_id = '3'`).Scan(&chat, &target); err != nil {
		t.Fatal(err)
	}
	if chat != pn.String() || target != lid.String() {
		t.Fatalf("chat=%s target=%s, want chat=%s target=%s", chat, target, pn, lid)
	}
	if got, ok := ms.LabelTargetJID(pn.String()); !ok || got != lid.String() {
		t.Fatalf("LabelTargetJID = %q %v", got, ok)
	}
	if !ms.PrefersLIDLabelTargets() {
		t.Fatal("expected LID preference when only LID targets are stored")
	}

	// Unlabel removes the association.
	handleLabelEvent(client, ms, &events.LabelAssociationChat{LabelID: "3", JID: lid, Timestamp: now,
		Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(false)}}, log)
	if n := countRows(t, ms, `SELECT COUNT(*) FROM chat_labels`); n != 0 {
		t.Fatalf("chat_labels after unlabel = %d", n)
	}

	// Deleting the label removes it and any associations.
	_ = ms.ApplyChatLabel("3", pn.String(), pn.String(), true, now)
	handleLabelEvent(client, ms, &events.LabelEdit{LabelID: "3", Timestamp: now,
		Action: &waSyncAction.LabelEditAction{Deleted: proto.Bool(true)}}, log)
	if n := countRows(t, ms, `SELECT COUNT(*) FROM labels`) + countRows(t, ms, `SELECT COUNT(*) FROM chat_labels`); n != 0 {
		t.Fatalf("rows after delete = %d", n)
	}
}

func TestLabelIDAndOrderAllocation(t *testing.T) {
	ms := newLabelTestStore(t)
	if o, _ := ms.NextLabelOrderIndex(); o != nil {
		t.Fatalf("order index should be omitted when no label uses ordering, got %d", *o)
	}
	if id, _ := ms.NextLabelID(); id != "1" {
		t.Fatalf("empty NextLabelID = %s", id)
	}
	for _, id := range []string{"2", "10", "abc"} {
		_ = ms.ApplyLabelEdit(id, &waSyncAction.LabelEditAction{Name: proto.String(id), OrderIndex: proto.Int32(7)}, time.Now())
	}
	if id, _ := ms.NextLabelID(); id != "11" {
		t.Fatalf("NextLabelID = %s, want 11", id)
	}
	if o, _ := ms.NextLabelOrderIndex(); o == nil || *o != 8 {
		t.Fatalf("NextLabelOrderIndex = %v, want 8", o)
	}
}

func TestPruneLabelsSyncedBefore(t *testing.T) {
	ms := newLabelTestStore(t)
	_ = ms.ApplyLabelEdit("1", &waSyncAction.LabelEditAction{Name: proto.String("old")}, time.Now())
	_ = ms.ApplyChatLabel("1", "a@s.whatsapp.net", "a@s.whatsapp.net", true, time.Now())
	cutoff := time.Now().Add(time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	_ = ms.ApplyLabelEdit("2", &waSyncAction.LabelEditAction{Name: proto.String("fresh")}, time.Now())
	if err := ms.PruneLabelsSyncedBefore(cutoff); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, ms, `SELECT COUNT(*) FROM labels WHERE id = '2'`); n != 1 {
		t.Fatal("fresh label pruned")
	}
	if n := countRows(t, ms, `SELECT COUNT(*) FROM labels WHERE id = '1'`) + countRows(t, ms, `SELECT COUNT(*) FROM chat_labels`); n != 0 {
		t.Fatal("stale rows kept")
	}
}

func TestLabelActionFromStoredRoundTrip(t *testing.T) {
	l := Label{ID: "5", Name: "VIP", Color: 2, ListType: "CUSTOM", OrderIndex: proto.Int32(3), IsActive: proto.Bool(true)}
	act := labelActionFromStored(l)
	if act.GetName() != "VIP" || act.GetColor() != 2 || act.GetType() != waSyncAction.LabelEditAction_CUSTOM ||
		act.GetOrderIndex() != 3 || !act.GetIsActive() || act.GetDeleted() {
		t.Fatalf("unexpected action %+v", act)
	}
	if err := validLabelColor(proto.Int32(labelColorCount)); err == nil {
		t.Fatal("expected out-of-range colour to fail")
	}
}

func TestRegularSyncCompletePrunesOnlyAfterRequestedSync(t *testing.T) {
	ms := newLabelTestStore(t)
	client := newTestClient(&mockLIDStore{})
	_ = ms.ApplyLabelEdit("1", &waSyncAction.LabelEditAction{Name: proto.String("stale")}, time.Now())

	// A sync the bridge didn't request (e.g. pair time) must not prune.
	labelSyncStart.Store(0)
	handleLabelEvent(client, ms, &events.AppStateSyncComplete{Name: appstate.WAPatchRegular}, testLogger())
	if n := countRows(t, ms, `SELECT COUNT(*) FROM labels`); n != 1 {
		t.Fatalf("unrequested sync pruned rows: %d left", n)
	}

	time.Sleep(time.Millisecond)
	labelSyncStart.Store(time.Now().UnixNano())
	client.EmitAppStateEventsOnFullSync = true
	time.Sleep(time.Millisecond)
	_ = ms.ApplyLabelEdit("2", &waSyncAction.LabelEditAction{Name: proto.String("fresh")}, time.Now())
	handleLabelEvent(client, ms, &events.AppStateSyncComplete{Name: appstate.WAPatchRegular, Recovery: true}, testLogger())
	if n := countRows(t, ms, `SELECT COUNT(*) FROM labels WHERE id = '2'`); n != 1 {
		t.Fatal("fresh label pruned")
	}
	if n := countRows(t, ms, `SELECT COUNT(*) FROM labels WHERE id = '1'`); n != 0 {
		t.Fatal("stale label kept after requested sync")
	}
	if labelSyncStart.Load() != 0 || client.EmitAppStateEventsOnFullSync {
		t.Fatal("sync marker / full-sync event emission not cleared")
	}
}

func TestIsBuiltInLabel(t *testing.T) {
	zero := proto.Int32(0)
	cases := []struct {
		l    Label
		want bool
	}{
		{Label{ListType: "CUSTOM", PredefinedID: zero}, false}, // personal custom list
		{Label{PredefinedID: proto.Int32(2)}, false},           // business predefined label
		{Label{ListType: "FAVORITES", PredefinedID: zero}, true},
		{Label{ListType: "UNREAD"}, true},
		{Label{ListType: "CUSTOM", IsImmutable: true}, true},
	}
	for _, c := range cases {
		if got := isBuiltInLabel(c.l); got != c.want {
			t.Errorf("isBuiltInLabel(%+v) = %v, want %v", c.l, got, c.want)
		}
	}
}
