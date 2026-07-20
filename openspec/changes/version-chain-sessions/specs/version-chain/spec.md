## ADDED Requirements

### Requirement: 自动版本链化
The system SHALL, on each successful session upload via `POST /api/sessions`, automatically link the new session to a version chain identified by the `(project, basename(entry_file), owner_id)` tuple. When the tuple already identifies an existing chain, the session SHALL be appended as the next version (`MAX(version_no) + 1`); otherwise a new chain SHALL be created with `version_no = 1`. The chain linking operation SHALL execute atomically in a single database transaction.

The system SHALL scope chains by owner when authentication is enabled: sessions created by different owners with the same project and entry-file basename SHALL belong to distinct chains. When authentication is disabled, the owner component SHALL be NULL and all sessions with the same `(project, entry_file)` SHALL share a single global chain.

When chain linking fails for any reason other than `ErrSessionNotFound`, the system SHALL NOT abort the upload; the session SHALL be persisted as an unlinked session and the failure SHALL be logged to standard error.

#### Scenario: 首次上传同一产物创建新链
- **WHEN** the agent uploads `index.html` under project `sales-dashboard` for the first time
- **THEN** the system creates a new `document_chains` row keyed by `(sales-dashboard, index.html, <owner>)`
- **AND** the session row's `version_no` is set to `1`
- **AND** the `chainId` and `versionNo` fields are present in the upload response

#### Scenario: 同产物的后续上传追加为下一版本
- **WHEN** the agent uploads `index.html` under project `sales-dashboard` again, from any source directory
- **THEN** the session is linked to the existing chain (no new chain row created)
- **AND** the session row's `version_no` is `MAX(existing version_no) + 1`

#### Scenario: 不同 owner 的同产物各自独立成链
- **WHEN** authentication is enabled and users `alice` and `bob` both upload `index.html` under project `sales-dashboard`
- **THEN** alice's uploads form one chain and bob's uploads form a separate chain
- **AND** neither chain contains the other user's sessions

#### Scenario: 链化失败降级不阻塞上传
- **WHEN** an internal error occurs during `LinkToChain` after a session has been successfully created
- **THEN** the upload response still returns `201 Created` with the session URL
- **AND** the session is persisted with `chain_id = NULL` and `version_no = NULL`
- **AND** an error message is written to standard error describing the chain-link failure

### Requirement: 版本链查询接口
The system SHALL expose `GET /api/sessions/{sessionId}/chain` returning the chain identity, all non-soft-deleted versions ordered by `version_no` ascending (with the current session marked via `current: true`), and the per-step metadata diff. The endpoint SHALL respect session ownership: under authentication, a request for another user's session SHALL return `403` (or `404` to avoid leaking existence), and a request for a non-existent session SHALL return `404`.

When the requested session is not linked to any chain, the endpoint SHALL return a synthesized single-version view with an empty `chainId`, the session's own metadata, `versions: [<{current session}>]`, and `metadataDiff: []`, so callers can render a degenerate "v1 of 1" view without special-casing.

#### Scenario: 查询多版本链
- **WHEN** a client requests `GET /api/sessions/<v3-id>/chain` for a chain with three live versions
- **THEN** the response contains `chain.versionNum = 3`, `versions` of length 3 ordered v1→v2→v3
- **AND** the version matching the requested session id has `current: true`
- **AND** `current` in the response top-level equals that version
- **AND** `metadataDiff` has one entry per version including v1 vs. an empty baseline

#### Scenario: 查询未链化 session 返回单版本合成视图
- **WHEN** a client requests the chain of a session whose `chain_id` is NULL (older data or chain-link failure)
- **THEN** the response status is `200`
- **AND** `chain.chainId` is empty
- **AND** `versions` has exactly one entry marked `current: true`
- **AND** `metadataDiff` is empty

#### Scenario: 查询不存在 session 返回 404
- **WHEN** a client requests `GET /api/sessions/nonexistent/chain`
- **THEN** the response status is `404`

#### Scenario: 跨 owner 查询被拒绝
- **WHEN** authentication is enabled and a user requests the chain of a session owned by another user
- **THEN** the response status is `403` (or `404` per the existing owner-isolation policy)

### Requirement: 相邻版本元数据 diff
The system SHALL compute, for each version in a chain, the metadata transition from its predecessor: set-relative `addedTags` / `removedTags`, and `categoryOld`→`categoryNew`, `projectOld`→`projectNew` transitions. The first version (v1) SHALL be compared against an empty baseline, so its initial tags appear under `addedTags` and its category / project appear as the `*New` fields with empty `*Old` fields.

The diff SHALL be ordered by `version_no` ascending. Soft-deleted versions SHALL be excluded from both the version list and the diff baseline; diff computation SHALL proceed as though the soft-deleted version never existed (i.e., the version after a soft-deleted one is diffed against the most recent live predecessor).

#### Scenario: v1 与空基线对比
- **WHEN** a chain has a single v1 with tags `[a, b]`, category `c1`, project `p`
- **THEN** `metadataDiff[0]` has `toVersion = 1`, `fromVersion = 0`, `addedTags = [a, b]`, `removedTags = []`, `categoryNew = "c1"`, `categoryOld = ""`

#### Scenario: 相邻版本 tag 增删
- **WHEN** v1 has tags `[a, b]` and v2 has tags `[a, c]`
- **THEN** the v2 diff entry has `addedTags = [c]` and `removedTags = [b]`

#### Scenario: category 变更
- **WHEN** v1 has category `c1` and v2 has category `c2`
- **THEN** the v2 diff entry has `categoryOld = "c1"` and `categoryNew = "c2"`

#### Scenario: 软删版本不参与 diff
- **WHEN** a chain has v1, v2, v3 and v2 is soft-deleted
- **THEN** the diff list contains entries for v1 and v3 only
- **AND** the v3 diff is computed against v1 (not against the deleted v2)

### Requirement: 软删除保留链结构
The system SHALL NOT cascade soft-delete of a session to other sessions in the same chain. The `document_chains` row SHALL survive the soft-delete of any or all of its member sessions. The deleted session's `version_no` SHALL be preserved (no renumbering); subsequent chain queries SHALL skip the soft-deleted version while leaving gaps in the version sequence.

#### Scenario: 删除中间版本后链保留
- **WHEN** a chain has v1, v2, v3 and v2 is soft-deleted
- **THEN** querying the chain returns `[v1, v3]`
- **AND** `v1.versionNo = 1` and `v3.versionNo = 3` (no renumbering)
- **AND** the `document_chains` row is still present

### Requirement: 预览页版本时间线
The system SHALL inject a version-timeline drawer into every HTML preview page served under `/s/{sessionId}/`. The injection SHALL reuse the existing `live.InjectMiddleware` HTML-buffering path and SHALL be additive to (not replace) the existing related-documents drawer.

The drawer SHALL be hidden by default. On page load it SHALL probe `GET /api/sessions/{sessionId}/chain`; the timeline button SHALL become visible only when the chain contains more than one live version, displaying the current version number (e.g. `v3`). Clicking the button SHALL open a side panel that renders the full timeline ordered newest-first, highlights the current version, and below each version shows its metadata diff relative to the previous live version. Selectors SHALL be namespaced under `#sth-version-*` / `.sth-version-*` and critical layout properties SHALL use `!important` to resist conflicts with the user's own stylesheets.

#### Scenario: 链长为 1 时不显示徽标
- **WHEN** a preview page is loaded for a session that is the only live version in its chain
- **THEN** no version-timeline button is visible

#### Scenario: 链长 > 1 时显示版本徽标
- **WHEN** a preview page is loaded for a session whose chain has 3 live versions and the current session is v3
- **THEN** a version button is visible showing the text `v3`

#### Scenario: 点击徽标展开时间线
- **WHEN** the user clicks the version button
- **THEN** a side panel opens listing all versions newest-first
- **AND** the current version is visually distinguished
- **AND** each version shows the metadata diff relative to the previous live version

#### Scenario: probe 失败静默降级
- **WHEN** the chain probe request fails (network error or non-200)
- **THEN** no version button is displayed
- **AND** no error is surfaced to the user
