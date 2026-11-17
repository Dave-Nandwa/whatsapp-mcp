"""Chat labels / WhatsApp Lists: reads from messages.db, writes via the bridge."""

import sqlite3

import pytest

import main
import whatsapp

ALICE = "1234567890@s.whatsapp.net"
BOB = "1987654321@s.whatsapp.net"


def _make_db(path, with_labels=True):
    conn = sqlite3.connect(path)
    conn.executescript(
        """
        CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
        CREATE TABLE messages (
            id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
            is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT, media_key BLOB,
            file_sha256 BLOB, file_enc_sha256 BLOB, file_length INTEGER,
            PRIMARY KEY (id, chat_jid)
        );
        """
    )
    for i, (jid, name) in enumerate([(ALICE, "Alice"), (BOB, "Bob")]):
        ts = f"2024-01-1{i + 1} 10:00:00+00:00"
        conn.execute("INSERT INTO chats VALUES (?, ?, ?)", (jid, name, ts))
        conn.execute(
            "INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES (?, ?, ?, ?, ?, 0)",
            (f"m{i}", jid, jid.split("@")[0], "hi", ts),
        )
    if with_labels:
        conn.executescript(
            """
            CREATE TABLE labels (
                id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', color INTEGER NOT NULL DEFAULT 0,
                predefined_id INTEGER, list_type TEXT, order_index INTEGER, is_active BOOLEAN,
                is_immutable BOOLEAN NOT NULL DEFAULT 0, updated_at TIMESTAMP, synced_at TIMESTAMP
            );
            CREATE TABLE chat_labels (
                label_id TEXT NOT NULL, chat_jid TEXT NOT NULL, target_jid TEXT NOT NULL,
                labeled_at TIMESTAMP, synced_at TIMESTAMP, PRIMARY KEY (label_id, chat_jid)
            );
            INSERT INTO labels (id, name, color, predefined_id, list_type, order_index, is_immutable)
                VALUES ('1', 'Favorites', 0, 0, 'FAVORITES', 0, 0),
                       ('5', 'Leads', 3, 0, 'CUSTOM', 1, 0),
                       ('8', char(8206) || 'Groups', 0, 0, 'GROUPS', 2, 0);
            """
        )
        conn.execute("INSERT INTO chat_labels (label_id, chat_jid, target_jid) VALUES ('5', ?, ?)", (ALICE, ALICE))
    conn.commit()
    conn.close()


@pytest.fixture
def db(tmp_path, monkeypatch):
    path = tmp_path / "messages.db"
    _make_db(str(path))
    monkeypatch.setattr(whatsapp, "MESSAGES_DB_PATH", str(path))
    return path


def test_list_labels_ordered_with_counts(db):
    labels = whatsapp.list_labels()
    assert [label["name"] for label in labels] == ["Favorites", "Leads", "Groups"]
    fav, leads, _groups = labels
    assert fav["built_in"] is True and fav["type"] == "FAVORITES"
    assert leads == {"id": "5", "name": "Leads", "color": 3, "type": "CUSTOM", "built_in": False, "chat_count": 1}


def test_resolve_label_by_id_or_name(db):
    assert whatsapp.resolve_label("5")["name"] == "Leads"
    assert whatsapp.resolve_label("leads")["id"] == "5"
    assert whatsapp.resolve_label("nope") is None


def test_list_chats_label_filter_and_labels_field(db):
    chats = whatsapp.list_chats(label="LEADS")
    assert [c["jid"] for c in chats] == [ALICE]
    assert chats[0]["labels"] == ["Leads"]

    by_jid = {c["jid"]: c for c in whatsapp.list_chats()}
    assert by_jid[BOB]["labels"] == []
    assert whatsapp.get_chat(ALICE)["labels"] == ["Leads"]


def test_older_store_without_label_tables(tmp_path, monkeypatch):
    path = tmp_path / "messages.db"
    _make_db(str(path), with_labels=False)
    monkeypatch.setattr(whatsapp, "MESSAGES_DB_PATH", str(path))
    assert whatsapp.list_labels() == []
    assert whatsapp.list_chats(label="Leads") == []
    assert all(c["labels"] == [] for c in whatsapp.list_chats())


class _Resp:
    def __init__(self, status, body):
        self.status_code = status
        self._body = body
        self.text = str(body)

    def json(self):
        return self._body


@pytest.fixture
def posts(monkeypatch):
    calls = []

    def fake_post(url, json=None, headers=None, timeout=None):
        calls.append((url, json))
        return _Resp(200, {"success": True, "label_id": json.get("label_id", "6")})

    monkeypatch.setattr(whatsapp.requests, "post", fake_post)
    return calls


def test_label_chat_resolves_name_to_id(db, posts):
    result = main.label_chat(BOB, "leads")
    assert result["success"] is True
    assert posts == [
        (f"{whatsapp.WHATSAPP_API_BASE_URL}/labels/chat", {"chat_jid": BOB, "label_id": "5", "labeled": True})
    ]


def test_unknown_label_does_not_call_bridge(db, posts):
    assert main.unlabel_chat(BOB, "missing")["success"] is False
    assert main.delete_label("missing")["success"] is False
    assert posts == []


def test_create_label_rejects_duplicate_name(db, posts):
    assert main.create_label("Leads")["success"] is False
    assert main.create_label("  ")["success"] is False
    assert main.create_label("VIP", color=2)["success"] is True
    assert posts[-1][1] == {"action": "create", "name": "VIP", "color": 2}


def test_bridge_error_message_is_surfaced(db, monkeypatch):
    monkeypatch.setattr(
        whatsapp.requests,
        "post",
        lambda *a, **k: _Resp(
            400, {"success": False, "message": 'Label "Favorites" is built in and cannot be changed'}
        ),
    )
    result = main.update_label("Favorites", new_name="Faves")
    assert result == {"success": False, "message": 'Label "Favorites" is built in and cannot be changed'}


def test_old_bridge_404_explains_rebuild(db, monkeypatch):
    class _PlainResp(_Resp):
        def json(self):
            raise ValueError("not json")

    monkeypatch.setattr(whatsapp.requests, "post", lambda *a, **k: _PlainResp(404, "404 page not found"))
    result = main.create_label("VIP")
    assert result["success"] is False
    assert "does not support labels" in result["message"]


def test_bidi_marked_builtin_names_resolve(db):
    groups = whatsapp.resolve_label("groups")
    assert groups["id"] == "8" and groups["name"] == "Groups" and groups["built_in"] is True


def test_list_label_chats_includes_chats_without_messages(db):
    conn = sqlite3.connect(db)
    conn.execute("INSERT INTO chat_labels (label_id, chat_jid, target_jid) VALUES ('5', '999@g.us', '999@g.us')")
    conn.commit()
    conn.close()
    result = main.list_label_chats("Leads")
    assert result["success"] is True
    jids = [c["jid"] for c in result["chats"]]
    assert jids == [ALICE, "999@g.us"]
    assert result["chats"][1]["has_stored_messages"] is False
    assert whatsapp.list_chats(label="Leads")[0]["jid"] == ALICE  # list_chats keeps activity-only semantics
