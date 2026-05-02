## 1. CSS Styles

- [ ] 1.1 Add `.delete-btn` styles to the embedded `<style>` block in `homePageTemplate` — small text button, low-contrast default, red color on hover, cursor pointer
- [ ] 1.2 Verify styles don't break existing layout (session link, timestamp, meta tags)

## 2. HTML Button

- [ ] 2.1 Add a `<button class="delete-btn" data-session-id="{{ .ID }}">×</button>` inside each session `<li>`, after the `<time>` element in the template range loop
- [ ] 2.2 Verify the button appears on each session list item without layout issues

## 3. JavaScript

- [ ] 3.1 Add an inline `<script>` block before `</body>` in the template that attaches click handlers to all `.delete-btn` elements using event delegation on `<ul>`
- [ ] 3.2 Implement the click handler: read `data-session-id`, show `confirm()` dialog with session name, call `fetch('/api/sessions/' + id, { method: 'DELETE' })` on confirm
- [ ] 3.3 On success (200 response), remove the parent `<li>` from the DOM; on failure, show `alert()` with error message

## 4. Verification

- [ ] 4.1 Run `go test ./...` to confirm no regressions
- [ ] 4.2 Manual smoke test: open homepage, click delete on a session, confirm it disappears; verify cancel does nothing; verify deleted session no longer appears after page reload
