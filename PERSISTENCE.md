# Workspace persistence and recovery

## Storage location and contents

Respect XDG_DATA_HOME; otherwise use `~/.local/share/cryptodesk/workspace.sqlite`.
Use user-only permissions where practical. Do not store in the Git repository, frontend
localStorage, executable directory, `/tmp`, or an application bundle read-only path.
User layout must survive frontend rebuilds and release upgrades.

Use SQLite for essential workspace state. A versioned validated JSON workspace in one row
is acceptable for v1; a fully normalized schema is not required. Example conceptual tables:

```text
schema_migrations(version, applied_at)
workspace_state(id=1, schema_version, revision, json_document, saved_at)
workspace_backups(id, revision, json_document, created_at, reason)
```

The app owns SQL migrations. The bundled JSON Schema defines the portable workspace shape,
not an already-created database schema. Use prepared statements, transactions, busy timeout,
and a single coordinated writer. WAL mode is appropriate for local SQLite but its companion
files matter for backups; never copy only an open `.sqlite` file and call it a safe backup.
Use SQLite's backup facilities or a documented consistent export. [S18, S19]

Recommended workspace durability: WAL + synchronous=FULL, verified with the selected driver.
Do not disable durability to improve candle-cache speed. A separate disposable candle-cache
DB can use a different policy. Keep a small bounded history of workspace backups, at least
before imports, reset, and migrations.

## Validation and limits

The workspace always contains 1–20 tabs and 1–9 actual charts per tab. Grid cell count must
be >= actual chart count and <= 9. Unique UUIDs, a valid activeTabId, supported enum values,
valid track weights and time ranges, and bounded string lengths are mandatory. Empty grid
cells are inferred; they are not entries in the charts array. See the schema plus semantic
rules in schema/README.md. Limits are app constants, not mutable fields in the imported JSON.

Differentiate no saved workspace from invalid/unreadable/corrupt/future-version data.
Only no saved workspace produces the one-tab/one-chart first-run state automatically.
On a saved-state error, preserve the source, show a recovery choice, and offer a valid backup
or explicit reset. Never silently overwrite a previously saved user workspace with defaults.

## Autosave and ordering

Capture every workspace mutation in one frontend store, including tab selection and viewport
updates. Debounce ordinary writes for 500 ms idle, with maximum 2 s deferral under continuous
interaction. Flush before disposing an active tab and on normal shutdown. Do not debounce
until the user stops interacting forever. Server responses update save status without
creating a new edit/save feedback loop.

Use a session ID, increasing frontend edit sequence, and persisted DB revision. Serialize
save requests, coalescing pending snapshots to the newest. Go rejects stale/out-of-order
writes and returns the committed sequence/revision. The frontend marks Saved only for that
acknowledged draft, not for newer unsent edits. Revision conflicts never cause last-writer
silent data loss. An OS-local single-instance lock prevents ordinary multi-process conflicts;
a second instance should focus the first where supported or exit with a helpful message.

Capture viewport changes before chart unmount. Keep transient loading/network state out of
the document. A backend callback or late history response cannot overwrite a newly edited
instrument or resurrect a removed chart.

## Normal shutdown handshake

Do not assume an asynchronous browser `beforeunload` can finish database writes. Use Wails'
controlled close lifecycle. For example, OnBeforeClose temporarily prevents close and sends
a prepare-close event; the frontend captures remaining viewport edits and awaits SaveWorkspace;
a token-checked confirmation then permits Quit. Protect against reentrant close events.
Do not block the GUI thread waiting for a frontend callback that cannot run. [S02]

Save failure keeps the app open with Retry / explicitly discard pending changes and quit.
Use a bounded timeout and clearly distinguish it from success. Backend-only shutdown flushes
already-received snapshots, cancels feeds, and closes the store. Exit must not wait forever
for an exchange network operation. A force kill can lose uncommitted edits; the promise is
restoration of the last acknowledged database commit, not impossible lossless crash capture.

## Startup and import/export

Acquire instance lock, open/migrate storage safely, validate workspace, then build the shell.
Restore exact saved counts, order, layout, scales and viewports; prioritize the selected tab.
Do not automatically import `capacity-20-tabs-180-charts.json`. It is for tests only.

Export a validated workspace JSON through a file dialog; include schemaVersion, no credentials,
no caches or runtime handles. Import is transactional replace in v1, not ambiguous merge.
Read at most 5 MiB, validate structure and semantics, preview counts, ask confirmation, save a
consistent backup, then replace atomically. Invalid imports must leave current state unchanged.
Future schemaVersion values are rejected without alteration. Migrations must be tested both
forward and against failed migration rollback.

Store absolute UTC time anchors for historical viewports. Persist bar spacing/right offset for
live mode. Keep display timezone separate. Persist price auto/log settings; optional manual
ranges require verified public API support in the pinned chart library. Record unsupported
platform details instead of accessing private chart internals.
