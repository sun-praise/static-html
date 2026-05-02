## Why

Sessions listed on the homepage can now be soft-deleted via the API (`DELETE /api/sessions/:id`) and CLI, but there is no way to trigger deletion from the web UI. Users must switch to the terminal to remove clutter, which breaks the in-browser workflow.

## What Changes

- Add a small delete button to each session list item on the homepage
- Add inline JavaScript to call the existing `DELETE /api/sessions/:id` endpoint and remove the item from the DOM on success
- Add a confirmation prompt before deleting

## Capabilities

### New Capabilities
- `web-session-delete`: Frontend delete interaction on the homepage — a small button per session that triggers soft-delete via the existing API, with confirmation and DOM removal

### Modified Capabilities

## Impact

- **Frontend**: The homepage Go template in `internal/server/server.go` gains a delete button, CSS styles, and an inline `<script>` block
- **API**: No changes — reuses the existing `DELETE /api/sessions/:id` endpoint
- **Code**: Only `internal/server/server.go` (the embedded template) is modified
