package main

// Chat labels.
//
// WhatsApp Business "labels" and personal WhatsApp "Lists" (Favorites, Unread,
// Groups, custom lists) are the same thing on the wire: label_edit and
// label_jid mutations in the "regular" app-state collection. whatsmeow decodes
// them into events.LabelEdit / events.LabelAssociationChat but does not persist
// them, so the bridge mirrors them into messages.db (labels, chat_labels) where
// the MCP server can read them without the bridge being up.
//
// Writes go through POST /api/labels and POST /api/labels/chat, which send an
// app-state patch. whatsmeow re-fetches the collection after a successful send
// and re-emits the event, so the event handler stays the single writer; the
// endpoints additionally upsert directly so a read right after the call is
// consistent.
//
// Existing labels are only delivered during a full app-state sync, which
// normally happens once at pair time. POST /api/labels/sync forces a full
// re-sync of the "regular" collection so already-paired bridges can backfill.
// If WhatsApp's server snapshot fails verification (mismatching LTHash, seen
// on long-lived personal accounts), the endpoint falls back to a fatal-recovery
// request: the phone sends an unencrypted copy of the collection, which
// whatsmeow applies and re-emits asynchronously.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// labelColorCount is the size of WhatsApp's label colour palette (0-19).
const labelColorCount = 20

const labelSchema = `
	CREATE TABLE IF NOT EXISTS labels (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		color INTEGER NOT NULL DEFAULT 0,
		predefined_id INTEGER,
		list_type TEXT,
		order_index INTEGER,
		is_active BOOLEAN,
		is_immutable BOOLEAN NOT NULL DEFAULT 0,
		updated_at TIMESTAMP,
		synced_at TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS chat_labels (
		label_id TEXT NOT NULL,
		chat_jid TEXT NOT NULL,
		target_jid TEXT NOT NULL,
		labeled_at TIMESTAMP,
		synced_at TIMESTAMP,
		PRIMARY KEY (label_id, chat_jid)
	);

	CREATE INDEX IF NOT EXISTS idx_chat_labels_chat ON chat_labels(chat_jid);
`

// Label is a stored label row.
type Label struct {
	ID           string
	Name         string
	Color        int32
	PredefinedID *int32
	ListType     string
	OrderIndex   *int32
	IsActive     *bool
	IsImmutable  bool
}

func nullInt32(v *int32) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullBool(v *bool) any {
	if v == nil {
		return nil
	}
	return *v
}

// ApplyLabelEdit stores or deletes a label from a label_edit action.
func (store *MessageStore) ApplyLabelEdit(labelID string, act *waSyncAction.LabelEditAction, ts time.Time) error {
	if labelID == "" || act == nil {
		return nil
	}
	now := time.Now()
	if act.GetDeleted() {
		tx, err := store.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM chat_labels WHERE label_id = ?`, labelID); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`DELETE FROM labels WHERE id = ?`, labelID); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	var listType any
	if act.Type != nil {
		listType = act.GetType().String()
	}
	_, err := store.db.Exec(`
		INSERT INTO labels (id, name, color, predefined_id, list_type, order_index, is_active, is_immutable, updated_at, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			color = excluded.color,
			predefined_id = excluded.predefined_id,
			list_type = excluded.list_type,
			order_index = excluded.order_index,
			is_active = excluded.is_active,
			is_immutable = excluded.is_immutable,
			updated_at = excluded.updated_at,
			synced_at = excluded.synced_at`,
		labelID, act.GetName(), act.GetColor(), nullInt32(act.PredefinedID), listType,
		nullInt32(act.OrderIndex), nullBool(act.IsActive), act.GetIsImmutable(), ts, now)
	return err
}

// ApplyChatLabel stores or removes a chat ↔ label association. chatJID is the
// normalised (phone) JID used elsewhere in messages.db; targetJID is the exact
// JID WhatsApp used in the mutation index, kept so later edits address the same
// index the phone does.
func (store *MessageStore) ApplyChatLabel(labelID, chatJID, targetJID string, labeled bool, ts time.Time) error {
	if labelID == "" || chatJID == "" {
		return nil
	}
	if !labeled {
		_, err := store.db.Exec(`DELETE FROM chat_labels WHERE label_id = ? AND chat_jid = ?`, labelID, chatJID)
		return err
	}
	_, err := store.db.Exec(`
		INSERT INTO chat_labels (label_id, chat_jid, target_jid, labeled_at, synced_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(label_id, chat_jid) DO UPDATE SET
			target_jid = excluded.target_jid,
			labeled_at = excluded.labeled_at,
			synced_at = excluded.synced_at`,
		labelID, chatJID, targetJID, ts, time.Now())
	return err
}

// PruneLabelsSyncedBefore removes rows a full sync did not re-deliver, i.e.
// labels or associations deleted while the bridge was not listening.
func (store *MessageStore) PruneLabelsSyncedBefore(cutoff time.Time) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM chat_labels WHERE synced_at IS NULL OR synced_at < ?`,
		`DELETE FROM labels WHERE synced_at IS NULL OR synced_at < ?`,
	} {
		if _, err := tx.Exec(q, cutoff); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// GetLabel returns a stored label or sql.ErrNoRows.
func (store *MessageStore) GetLabel(id string) (Label, error) {
	var l Label
	var predefined, order sql.NullInt32
	var listType sql.NullString
	var active sql.NullBool
	err := store.db.QueryRow(`
		SELECT id, name, color, predefined_id, list_type, order_index, is_active, is_immutable
		FROM labels WHERE id = ?`, id).Scan(&l.ID, &l.Name, &l.Color, &predefined, &listType, &order, &active, &l.IsImmutable)
	if err != nil {
		return l, err
	}
	if predefined.Valid {
		l.PredefinedID = &predefined.Int32
	}
	if order.Valid {
		l.OrderIndex = &order.Int32
	}
	if active.Valid {
		l.IsActive = &active.Bool
	}
	l.ListType = listType.String
	return l, nil
}

// NextLabelID returns one more than the highest numeric label ID, matching how
// WhatsApp clients allocate IDs.
func (store *MessageStore) NextLabelID() (string, error) {
	rows, err := store.db.Query(`SELECT id FROM labels`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	maxID := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if n, err := strconv.Atoi(id); err == nil && n > maxID {
			maxID = n
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return strconv.Itoa(maxID + 1), nil
}

// NextLabelOrderIndex returns an order index that places a new label last, or
// nil when no stored label uses ordering (personal-account Lists don't), so
// new labels match the phone's own shape.
func (store *MessageStore) NextLabelOrderIndex() (*int32, error) {
	var maxOrder sql.NullInt32
	if err := store.db.QueryRow(`SELECT MAX(order_index) FROM labels`).Scan(&maxOrder); err != nil {
		return nil, err
	}
	if !maxOrder.Valid {
		return nil, nil
	}
	return proto.Int32(maxOrder.Int32 + 1), nil
}

// isBuiltInLabel reports whether a label is one of WhatsApp's own lists
// (Favourites, Unread, Groups, ...), which clients don't let users edit.
// PredefinedID is not used: phones send predefinedID=0 on custom lists too.
func isBuiltInLabel(l Label) bool {
	if l.IsImmutable {
		return true
	}
	switch l.ListType {
	case "", "NONE", "CUSTOM":
		return false
	}
	return true
}

// LabelTargetJID returns the JID form previously used by WhatsApp for this chat
// in any label association, so new associations reuse the phone's addressing.
func (store *MessageStore) LabelTargetJID(chatJID string) (string, bool) {
	var target string
	err := store.db.QueryRow(`SELECT target_jid FROM chat_labels WHERE chat_jid = ? ORDER BY labeled_at DESC LIMIT 1`, chatJID).Scan(&target)
	if err != nil || target == "" {
		return "", false
	}
	return target, true
}

// PrefersLIDLabelTargets reports whether the phone addresses DM label
// associations by LID. Decided from what has been synced: if any stored DM
// association uses @lid and none uses a phone JID, new ones use LID too.
func (store *MessageStore) PrefersLIDLabelTargets() bool {
	var lid, pn int
	_ = store.db.QueryRow(`SELECT
			COALESCE(SUM(CASE WHEN target_jid LIKE '%@lid' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN target_jid LIKE '%@s.whatsapp.net' THEN 1 ELSE 0 END), 0)
		FROM chat_labels`).Scan(&lid, &pn)
	return lid > 0 && pn == 0
}

// labelChatJID normalises a label-association target to the chat JID used in
// messages.db (LID DMs are stored under the phone JID when known).
func labelChatJID(client *whatsmeow.Client, target types.JID) types.JID {
	return resolveUserJID(client, target, types.EmptyJID)
}

// labelSyncWindow bounds how long EmitAppStateEventsOnFullSync stays on after
// a requested label sync, in case the phone never answers a recovery request.
const labelSyncWindow = 5 * time.Minute

// beginLabelSync turns on full-sync event emission only for the duration of a
// requested label sync. It is off by default so pair-time syncs (contacts
// etc.) keep whatsmeow's default fast path untouched.
func beginLabelSync(client *whatsmeow.Client) {
	start := time.Now().UnixNano()
	labelSyncStart.Store(start)
	client.EmitAppStateEventsOnFullSync = true
	time.AfterFunc(labelSyncWindow, func() {
		if labelSyncStart.CompareAndSwap(start, 0) {
			client.EmitAppStateEventsOnFullSync = false
		}
	})
}

func endLabelSync(client *whatsmeow.Client) {
	labelSyncStart.Store(0)
	client.EmitAppStateEventsOnFullSync = false
}

// labelSyncStart holds the UnixNano start of the last /api/labels/sync, so
// the completion event can prune rows that the resync did not re-deliver.
var labelSyncStart atomic.Int64

// handleLabelEvent persists label events. Returns true if evt was a label event.
func handleLabelEvent(client *whatsmeow.Client, messageStore *MessageStore, evt any, logger waLog.Logger) bool {
	switch v := evt.(type) {
	case *events.LabelEdit:
		if err := messageStore.ApplyLabelEdit(v.LabelID, v.Action, v.Timestamp); err != nil {
			logger.Warnf("Failed to store label %s: %v", v.LabelID, err)
		}
		return true
	case *events.LabelAssociationChat:
		chat := labelChatJID(client, v.JID)
		if err := messageStore.ApplyChatLabel(v.LabelID, chat.String(), v.JID.String(), v.Action.GetLabeled(), v.Timestamp); err != nil {
			logger.Warnf("Failed to store label %s for chat %s: %v", v.LabelID, chat, err)
		}
		return true
	case *events.AppStateSyncComplete:
		if v.Name == appstate.WAPatchRegular {
			if start := labelSyncStart.Swap(0); start != 0 {
				client.EmitAppStateEventsOnFullSync = false
				if err := messageStore.PruneLabelsSyncedBefore(time.Unix(0, start)); err != nil {
					logger.Warnf("Failed to prune stale labels: %v", err)
				}
			}
		}
		return false // other handlers may care too
	}
	return false
}

// LabelRequest is the JSON body for POST /api/labels.
type LabelRequest struct {
	Action  string `json:"action"` // create | update | delete
	LabelID string `json:"label_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Color   *int32 `json:"color,omitempty"`
}

// ChatLabelRequest is the JSON body for POST /api/labels/chat.
type ChatLabelRequest struct {
	LabelID string `json:"label_id"`
	ChatJID string `json:"chat_jid"`
	Labeled bool   `json:"labeled"`
}

// buildLabelEditPatch builds a label_edit mutation. Unlike
// appstate.BuildLabelEdit it carries type/order/active, which personal
// WhatsApp needs to show the label as a List.
func buildLabelEditPatch(id string, act *waSyncAction.LabelEditAction) appstate.PatchInfo {
	return appstate.PatchInfo{
		Type: appstate.WAPatchRegular,
		Mutations: []appstate.MutationInfo{{
			Index:   []string{appstate.IndexLabelEdit, id},
			Version: 3,
			Value:   &waSyncAction.SyncActionValue{LabelEditAction: act},
		}},
	}
}

func labelActionFromStored(l Label) *waSyncAction.LabelEditAction {
	act := &waSyncAction.LabelEditAction{
		Name:         proto.String(l.Name),
		Color:        proto.Int32(l.Color),
		PredefinedID: l.PredefinedID,
		OrderIndex:   l.OrderIndex,
		IsActive:     l.IsActive,
		Deleted:      proto.Bool(false),
	}
	if v, ok := waSyncAction.LabelEditAction_ListType_value[l.ListType]; ok {
		act.Type = waSyncAction.LabelEditAction_ListType(v).Enum()
	}
	if l.IsImmutable {
		act.IsImmutable = proto.Bool(true)
	}
	return act
}

func validLabelColor(c *int32) error {
	if c != nil && (*c < 0 || *c >= labelColorCount) {
		return fmt.Errorf("color must be between 0 and %d", labelColorCount-1)
	}
	return nil
}

// resolveLabelTarget picks the JID to put in a label_jid mutation index.
func resolveLabelTarget(client *whatsmeow.Client, messageStore *MessageStore, chat types.JID) types.JID {
	if prev, ok := messageStore.LabelTargetJID(chat.String()); ok {
		if j, err := types.ParseJID(prev); err == nil {
			return j
		}
	}
	if chat.Server == types.DefaultUserServer && messageStore.PrefersLIDLabelTargets() &&
		client != nil && client.Store != nil && client.Store.LIDs != nil {
		if lid, err := client.Store.LIDs.GetLIDForPN(context.Background(), chat); err == nil && !lid.IsEmpty() {
			return lid
		}
	}
	return chat
}

func registerLabelEndpoints(mux *http.ServeMux, auth func(http.HandlerFunc) http.HandlerFunc, client *whatsmeow.Client, messageStore *MessageStore) {
	writeJSON := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	fail := func(w http.ResponseWriter, status int, msg string) {
		writeJSON(w, status, map[string]any{"success": false, "message": msg})
	}
	ready := func(w http.ResponseWriter) bool {
		if !client.IsConnected() {
			fail(w, http.StatusServiceUnavailable, "Not connected to WhatsApp")
			return false
		}
		if client.Store == nil || client.Store.ID == nil {
			fail(w, http.StatusServiceUnavailable, "Client is not paired")
			return false
		}
		return true
	}

	mux.HandleFunc("/api/labels/sync", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !ready(w) {
			return
		}
		beginLabelSync(client)
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		// Full sync re-delivers every label and association; events are
		// dispatched synchronously (including AppStateSyncComplete, which
		// prunes stale rows), so the tables are current on return.
		if err := client.FetchAppState(ctx, appstate.WAPatchRegular, true, false); err != nil {
			if !errors.Is(err, appstate.ErrMismatchingLTHash) {
				endLabelSync(client)
				fail(w, http.StatusBadGateway, fmt.Sprintf("Label sync failed: %v", err))
				return
			}
			// Server snapshot is corrupt for this account: ask the phone.
			fmt.Printf("Label sync: server snapshot failed verification (%v); requesting recovery from phone\n", err)
			if _, err := client.SendPeerMessage(ctx, whatsmeow.BuildAppStateRecoveryRequest(appstate.WAPatchRegular)); err != nil {
				endLabelSync(client)
				fail(w, http.StatusBadGateway, fmt.Sprintf("Label sync failed and recovery request could not be sent: %v", err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true, "pending": true,
				"message": "WhatsApp's server copy of your labels failed verification, so the bridge asked your phone " +
					"to resend it. Keep the phone online; labels should appear within a minute (check list_labels).",
			})
			return
		}
		var labels, assoc int
		_ = messageStore.db.QueryRow(`SELECT COUNT(*) FROM labels`).Scan(&labels)
		_ = messageStore.db.QueryRow(`SELECT COUNT(*) FROM chat_labels`).Scan(&assoc)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "labels": labels, "chat_labels": assoc})
	}))

	mux.HandleFunc("/api/labels", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req LabelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, "Invalid request format")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if err := validLabelColor(req.Color); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}

		var id string
		var act *waSyncAction.LabelEditAction
		switch req.Action {
		case "create":
			if req.Name == "" {
				fail(w, http.StatusBadRequest, "name is required")
				return
			}
			var err error
			if id, err = messageStore.NextLabelID(); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			order, err := messageStore.NextLabelOrderIndex()
			if err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			color := req.Color
			if color == nil {
				n, _ := strconv.Atoi(id)
				color = proto.Int32(int32(n % labelColorCount))
			}
			act = &waSyncAction.LabelEditAction{
				Name:       proto.String(req.Name),
				Color:      color,
				Deleted:    proto.Bool(false),
				OrderIndex: order,
				IsActive:   proto.Bool(true),
				// Phones send predefinedID=0 on custom lists; mirror that.
				PredefinedID: proto.Int32(0),
				Type:         waSyncAction.LabelEditAction_CUSTOM.Enum(),
			}
		case "update", "delete":
			if req.LabelID == "" {
				fail(w, http.StatusBadRequest, "label_id is required")
				return
			}
			existing, err := messageStore.GetLabel(req.LabelID)
			if errors.Is(err, sql.ErrNoRows) {
				fail(w, http.StatusNotFound, "Unknown label_id; run a label sync if it was created on another device")
				return
			} else if err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			if isBuiltInLabel(existing) {
				fail(w, http.StatusBadRequest, fmt.Sprintf("Label %q is built in and cannot be changed", existing.Name))
				return
			}
			id = existing.ID
			act = labelActionFromStored(existing)
			if req.Action == "delete" {
				act.Deleted = proto.Bool(true)
			} else {
				if req.Name == "" && req.Color == nil {
					fail(w, http.StatusBadRequest, "name or color is required")
					return
				}
				if req.Name != "" {
					act.Name = proto.String(req.Name)
				}
				if req.Color != nil {
					act.Color = req.Color
				}
			}
		default:
			fail(w, http.StatusBadRequest, "action must be create, update, or delete")
			return
		}

		if !ready(w) {
			return
		}
		if err := client.SendAppState(r.Context(), buildLabelEditPatch(id, act)); err != nil {
			fail(w, http.StatusBadGateway, fmt.Sprintf("Failed to update label: %v", err))
			return
		}
		if err := messageStore.ApplyLabelEdit(id, act, time.Now()); err != nil {
			fail(w, http.StatusInternalServerError, fmt.Sprintf("Label sent but not stored locally: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "label_id": id, "name": act.GetName(), "color": act.GetColor(), "deleted": act.GetDeleted(),
		})
	}))

	mux.HandleFunc("/api/labels/chat", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req ChatLabelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, "Invalid request format")
			return
		}
		if req.LabelID == "" || req.ChatJID == "" {
			fail(w, http.StatusBadRequest, "label_id and chat_jid are required")
			return
		}
		parsed, err := types.ParseJID(req.ChatJID)
		if err != nil || parsed.User == "" {
			fail(w, http.StatusBadRequest, "Invalid chat_jid")
			return
		}
		if _, err := messageStore.GetLabel(req.LabelID); errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "Unknown label_id; run a label sync if it was created on another device")
			return
		} else if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ready(w) {
			return
		}
		chat := labelChatJID(client, parsed.ToNonAD())
		target := resolveLabelTarget(client, messageStore, chat)
		if err := client.SendAppState(r.Context(), appstate.BuildLabelChat(target, req.LabelID, req.Labeled)); err != nil {
			fail(w, http.StatusBadGateway, fmt.Sprintf("Failed to update chat label: %v", err))
			return
		}
		if err := messageStore.ApplyChatLabel(req.LabelID, chat.String(), target.String(), req.Labeled, time.Now()); err != nil {
			fail(w, http.StatusInternalServerError, fmt.Sprintf("Label sent but not stored locally: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "label_id": req.LabelID, "chat_jid": chat.String(), "labeled": req.Labeled,
		})
	}))
}
