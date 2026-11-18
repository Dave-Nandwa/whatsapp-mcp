# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 0.7.0 (2026-09-23)


### Features

* **bridge:** skip status media and add WHATSAPP_AUTO_DOWNLOAD_MEDIA opt-out (#237) (23a6cf9)
* **mcp:** add view_media so media is reachable without a filesystem (#246) (bfa2c08)
* **mcp:** transcribe voice notes with configurable providers (#247) (b1c45b4)


### Bug Fixes

* **bridge:** document filename leaked the sender's absolute path (#218) (143d7a7)
* **bridge:** guard resolveLIDChat against a client without a LID store (#230) (88ba809)
* **bridge:** keep launchd monitor alive when the token file read is denied (#250) (4e10737)
* **bridge:** persist upload metadata for outbound media (#221) (9b82499)
* **bridge:** preserve message ID in text webhooks (#220) (e207fd3)
* **bridge:** render every rotated pairing QR code (#243) (0076aa4)
* **bridge:** skip media downloads when the message row failed to store (#231) (8ee9cb6)
* **bridge:** tighten store and media permissions to owner-only (#241) (a269163)
* **deps:** bump whatsmeow for companion registration (#244) (2de9d0a)
* **mcp:** remove the converted audio temp file after send (#227) (5fc9df2)


### Documentation

* explain where runtime data is stored and how to relocate it (#240) (ce06368)

## 0.6.0 (2026-08-11)


### Features

* add explicit message read receipts (#201) (65751f1)
* **bridge:** add outbound webhook opt-out (#204) (a9be700)
* **bridge:** persist chat read state from read receipts + history-sync backfill (#155) (a677828)
* optional caption on send_file (#193) (21df01f)


### Bug Fixes

* harden read-state observe/act after #155 and #201 (#203) (f0892be)

## 0.5.1 (2026-08-08)


### Bug Fixes

* **deps:** preserve Intel macOS cryptography installs (#188) (3ed0538)

## 0.5.0 (2026-08-08)


### Features

* **bridge:** add on-demand history sync for a single chat (#168) (43afd2e)
* **bridge:** configurable linked-device name via WHATSAPP_DEVICE_NAME (#157) (eb0565d), closes #156
* **mcp:** add @-mention support to send_message (#190) (30d2bdb)

## 0.4.2 (2026-08-05)


### Bug Fixes

* **bridge:** authenticate outbound webhook POSTs (e5f1a9a)
* **bridge:** bump whatsmeow for client compatibility (#182) (5470342)
* **bridge:** forward native WhatsApp activation metadata (#173) (afd2c7a)
* **bridge:** preserve original timestamp on retry-redelivered messages (#149) (bb55a54)
* exit orphaned stdio MCP servers on parent death (#177) (8cc0ecd)

## 0.4.1 (2026-06-26)


### Bug Fixes

* **mcp:** resolve bridge token from whatsmeow db path (000b8e7), closes #142

## 0.4.0 (2026-06-26)


### Features

* **bridge:** add support for stickers (#110) (f7f475c)
* **bridge:** forward reaction webhook events (#129) (22a03c2)
* capture inbound reactions and add /api/react endpoint (#108) (a7dc663), closes #106
* **mcp:** support http and sse transports via env var (#112) (0a9ba2b)


### Bug Fixes

* **bridge:** bump whatsmeow so WhatsApp accepts new device pairing (#128) (715be9d)
* **bridge:** extract text from Template, Button, Interactive, and List messages (#134) (d5db52e)
* **bridge:** fall back to local whatsmeow_contacts for chat name resolution (#135) (37df5b3)
* **bridge:** keep CDN auth tokens in directPath to fix 403 media downloads (#132) (1b67982)
* escape monitor-script shell vars in launchd installer (#130) (c758f5f)


### Performance Improvements

* **bridge:** index messages(chat_jid) to speed up LID migration (#144) (9c6fa66)


### Documentation

* make AGENTS.md canonical and fix FORWARD_SELF default (#127) (8c92254)
* **readme:** add demo video overview (#122) (11672a3)

## 0.3.0 (2026-06-03)


### Features

* **bridge:** add macOS launchd installer (#116) (9b2cb8a)
* persist inbound quoted_message_id and add reply support to /api/send (#109) (f72d352), closes #107


### Bug Fixes

* **bridge:** handle ProtocolMessage_REVOKE (delete-for-everyone) events (#99) (7f4ec42)
* **mcp:** deduplicate contact chat results (da543ce)
* **mcp:** serialize message context result (ebb1e07)


### Documentation

* **readme:** document app state recovery (#117) (040f9e1)

## 0.2.1 (2026-05-14)


### Bug Fixes

* **bridge:** log send caller identity (#96) (c81947c)
* **bridge:** pass message store when sending messages (#91) (423ada9)
* **bridge:** set FileName and detect MIME for document sends (#95) (af18908)
* **ci:** make dependabot auto-approve non-fatal (#65) (ea0dfb1)
* send and track disappearing-message settings correctly (#82) (b250409)
* **server:** list_chats/get_chat error when include_last_message=False (#79) (31e03e6)


### Documentation

* add contributors section and updating instructions to README (#67) (71b0c03)
* Update SECURITY.md with enhanced security policy (#78) (214780f)

## 0.2.0 (2026-04-22)


### Features

* add image media support in webhook forwarding (#45) (c5f409c)
* **bridge:** --full-history-pair flag to request full history at pair time (#37) (2358dd5)
* **bridge:** capture incoming WhatsApp call events (#39) (197a0c9)


### Bug Fixes

* **bridge:** auto-download runs after StoreMessage to avoid lookup race (#41) (0c267f5)
* **bridge:** handle StreamReplaced event to recover from session conflicts (#27) (0cd6475)
* **bridge:** include message ID in media filenames to prevent same-second collisions (#40) (1e819c1)
* **bridge:** resolve [@lid](https://github.com/lid) sender to phone JID in webhook payload (#56) (746fca0)
* **bridge:** surface image/video/document captions in extractTextContent (#42) (fbb3f28)
* **mcp:** match messages by both phone number and LID via whatsmeow_lid_map (#43) (04c8755)
* **mcp:** resolve contacts via whatsmeow store with LID → phone fallback (#30) (b9b0175)
* pin `anyio<4.9` to avoid cancel scope regression (#44) (627db67)
* security hardening for LAN exposure and Unicode search (#55) (8097f39)


### Documentation

* add ROADMAP, AGENTS, CONTRIBUTING, CODEOWNERS, issue/PR templates (#47) (c2e4f5b)
* document WHATSMEOW_DB_PATH env var (#51) (c24d151)
* update remaining "go run main.go" references to "go run ." (#50) (53e1ca7)

## 0.1.0 (2026-03-02)

### Added

**Go Bridge:**

- `/api/typing` endpoint - Send typing indicators to chats
- `/api/health` endpoint - Check WhatsApp connection status
- Webhook system for incoming messages (`webhook.go`)
  - Configurable via `WEBHOOK_URL` environment variable
  - Includes quoted message info (reply context)
  - `FORWARD_SELF` option to include self-sent messages
- Auto-download of media files when messages arrive
- Quoted message extraction (reply-to context: ID, sender, content)
- Connection status checks before API operations
- HTTP server timeouts for stability (read: 30s, write: 60s, idle: 120s)

**Python MCP Server:**

- `get_contact` tool - Resolve phone number to contact name
- `sender_display` field in messages - Shows "Name (phone)" format
- `sender_phone` field - Extracted phone number from JID
- Environment variable configuration (`WHATSAPP_DB_PATH`, `WHATSAPP_API_URL`)
- Test suite with pytest (8 tests)
- `sort_by` parameter for `list_messages` ("newest" or "oldest")
- Increased default limits (20 → 50 messages/chats)
- Maximum limits (500 messages, 200 chats)

**Infrastructure:**

- CI/CD pipeline with GitHub Actions (lint, build, test)
- Release Please workflow for automated release PRs/tags/changelog (`.github/workflows/release-please.yml`)
- Manual fallback release workflow for artifact re-publish (`.github/workflows/release.yml`)
- Version consistency check across `pyproject.toml`, `server.json`, and release tags
- Comprehensive documentation (README, CLAUDE.md)
- Maintainer release playbook (`docs/RELEASING.md`)
- Environment variable examples (`.env.example`)

### Fixed

- Go compilation errors from whatsmeow API changes (added `context.Background()`)
- Media filename consistency - now uses message timestamp instead of download time
- Startup migration now consolidates legacy `@lid` chat/message rows into mapped phone JIDs (`@s.whatsapp.net`) to prevent split chat history
- golangci.yml configuration (removed deprecated linters)
- Python linting issues (185 errors fixed)
- Trailing whitespace in SQL queries

### Changed

- Upgraded whatsmeow to latest version (v0.0.0-20260107124630)
- Upgraded Go linting pipeline to golangci-lint v2 (CI action + v2 config schema)
- Modernized Python type hints (`Optional[str]` → `str | None`)
- Improved docstrings with date format examples
- Media download uses message timestamp for consistent filenames
- Refactored `send_message` to use unified `recipient` parameter
- Adopted Release Please for automated versioning/changelog (`#15`) (6d45958)

### Removed

- Unused `package.json` from Go bridge (was for wrangler, not needed)
- Compiled binary from git tracking

---

## Fork History

This project is a fork of [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp), originally created by [Luke Harries](https://github.com/lharries) in 2024.

The original repository was last updated in April 2025. This fork by Very Good Plugins continues active development with bug fixes, new features, and MCP registry publication.
