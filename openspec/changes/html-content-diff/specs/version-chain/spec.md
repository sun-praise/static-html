## ADDED Requirements

### Requirement: HTML 内容 diff 接口
The system SHALL expose `GET /api/sessions/{sessionId}/diff?from={vN}&to={vM}` computing a line-level diff between the entry HTML of two versions in the same chain. The `{sessionId}` in the path identifies any session in the chain (used to resolve chain membership and ownership); `from` and `to` are 1-based version numbers of live versions within that chain.

The diff SHALL be computed by a longest-common-subsequence algorithm over the lines of each version's stored entry HTML file, producing an ordered list of line operations each classified as `equal`, `add`, or `delete`, with 1-based line numbers in the old (`oldNo`) and new (`newNo`) documents (0 when not applicable). The response SHALL include an additive summary (`added`, `removed` counts).

The endpoint SHALL respect session ownership identically to the chain endpoint. When either version number does not correspond to a live version in the chain, or `from` equals `to`, or a parameter is missing or non-positive, the system SHALL return `400`. When the path session does not exist or is soft-deleted, the system SHALL return `404`.

The diff computation SHALL be bounded: when either input exceeds a fixed line threshold, the system SHALL return a single sentinel line indicating the diff was skipped, rather than attempting the O(n*m) computation.

#### Scenario: 相邻版本的内容 diff
- **WHEN** a client requests `GET /api/sessions/<v2-id>/diff?from=1&to=2` for a two-version chain where v2 added one line
- **THEN** the response status is `200`
- **AND** `lines` contains one entry with `kind: "add"` carrying the added line's text and a positive `newNo`
- **AND** `summary.added` is at least 1 and `summary.removed` is 0
- **AND** `fromVersion` is 1 and `toVersion` is 2

#### Scenario: 缺少 from 或 to 参数返回 400
- **WHEN** a client requests `GET /api/sessions/<id>/diff?from=1` (missing `to`)
- **THEN** the response status is `400`

#### Scenario: 版本号越界返回 400
- **WHEN** a client requests `from=1&to=99` on a chain with only two live versions
- **THEN** the response status is `400`

#### Scenario: from 等于 to 返回 400
- **WHEN** a client requests `from=1&to=1`
- **THEN** the response status is `400`

#### Scenario: 不存在的 session 返回 404
- **WHEN** a client requests `GET /api/sessions/nonexistent/diff?from=1&to=2`
- **THEN** the response status is `404`

#### Scenario: 软删 session 返回 404
- **WHEN** the path session has been soft-deleted
- **THEN** the response status is `404` (consistent with the chain endpoint's soft-delete visibility rule)

#### Scenario: 老 session 文件缺失时降级为空内容
- **WHEN** a version's stored entry file is missing from disk (legacy session or cleared uploads)
- **THEN** the diff does not error
- **AND** the missing side is treated as empty content (its lines register as deletions or additions relative to the other side)

#### Scenario: 超大文件跳过 diff
- **WHEN** either version's entry HTML exceeds the line threshold
- **THEN** the response status is `200`
- **AND** `lines` contains a single sentinel entry indicating the diff was skipped due to size

### Requirement: 时间线 HTML diff 内联渲染
The system SHALL inject, into the version-timeline drawer of each preview page, a "Show HTML diff" control on every timeline item that has a live predecessor version. Activating the control SHALL lazily fetch the content diff between the item and its predecessor and render it inline within the drawer, with added lines styled distinctly from removed lines, and consecutive unchanged lines collapsed so changes remain visible without scrolling.

The predecessor SHALL be resolved as the live version with the largest version number strictly smaller than the current item's, so that soft-delete gaps (e.g. v1, v3 with v2 removed) still link v3 to v1. When a version has no live predecessor, no diff control SHALL be rendered.

#### Scenario: 有前驱的版本显示 diff 按钮
- **WHEN** the timeline renders a version that has a live predecessor
- **THEN** a "Show HTML diff" button is visible on that timeline item
- **AND** the button carries the predecessor and current version numbers

#### Scenario: 无前驱的版本不显示 diff 按钮
- **WHEN** the timeline renders the oldest live version (no predecessor)
- **THEN** no "Show HTML diff" button is rendered on that item

#### Scenario: 点击按钮懒加载并展开 diff
- **WHEN** the user clicks the "Show HTML diff" button
- **THEN** the diff endpoint is fetched for that version pair
- **AND** the result renders inline with added and removed lines visually distinguished
- **AND** consecutive unchanged lines are collapsed into a summary line rather than displayed individually

#### Scenario: 再次点击折叠
- **WHEN** the user clicks an already-expanded diff button again
- **THEN** the diff content is collapsed without re-fetching

#### Scenario: 软删空洞跳到更早前驱
- **WHEN** a chain has live versions v1 and v3 (v2 soft-deleted)
- **THEN** the v3 timeline item's "Show HTML diff" button compares v3 against v1

#### Scenario: diff 拉取失败显示错误
- **WHEN** the diff fetch returns a non-200 status or network error
- **THEN** the inline container shows an error message and does not crash the timeline
