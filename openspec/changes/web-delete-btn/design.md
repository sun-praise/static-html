## Context

The homepage is a Go template embedded in `internal/server/server.go` (lines 74–268). Each session renders as a `<li>` with a link, timestamp, and metadata tags. The backend already supports `DELETE /api/sessions/:id` which calls `store.SoftDelete` and returns 200/404. No frontend JS currently exists on the homepage.

## Goals / Non-Goals

**Goals:**
- Add a small delete button to each session list item
- Use inline JavaScript to call the existing DELETE endpoint
- Confirm before deleting and remove the item from the DOM on success
- Keep the UI minimal and consistent with the existing design

**Non-Goals:**
- Bulk delete / select multiple sessions
- Undo/restore deleted sessions from the UI
- Dedicated delete page or modal component library
- Server-side rendering of delete state (the button always appears for listed items)

## Decisions

**1. Inline `<script>` at the bottom of the template**

A single `<script>` block before `</body>` handles all delete interactions. No external JS files or frameworks — the homepage is currently zero-JS and a small inline script keeps it self-contained.

*Alternative considered:* External JS file served via `embed.FS` — rejected as over-engineering for a single interaction.

**2. Button rendered as a small `<button>` inside each `<li>`**

Place a small "×" or "delete" button after the timestamp, styled as a subtle text button with a red hover. On click, `confirm()` prompts the user, then `fetch()` calls `DELETE /api/sessions/:id`.

**3. Use `data-session-id` attribute on the button**

Each button carries the session ID in a `data-session-id` attribute. The click handler reads it via `event.target.dataset.sessionId`, constructs the API URL, and fires the request.

**4. DOM removal on success**

On a successful 200 response, remove the parent `<li>` from the DOM with `li.remove()`. On error (non-200), show a brief `alert()` with the error message.

## Risks / Trade-offs

- **No CSRF protection** → The API currently has no CSRF tokens. Acceptable for a local/dev tool. If the server is ever exposed publicly, CSRF protection should be added first.
- **`confirm()` is blocking** → Native `confirm()` is simple but not customizable. Acceptable for this scope; a custom modal could replace it later.
- **Template grows slightly** → Adding ~30 lines of JS to an embedded template. The alternative (external file) adds serving complexity that isn't justified.
