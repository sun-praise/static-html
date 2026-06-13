## ADDED Requirements

### Requirement: Floating button injection
The system SHALL inject a floating button into every HTML preview page served at `/s/{id}/`. The button SHALL be positioned at the bottom-right corner of the viewport with `position: fixed`.

#### Scenario: Button visible on preview page
- **WHEN** a user opens `/s/abc123/` which serves an HTML file
- **THEN** a floating button SHALL be visible at the bottom-right corner of the viewport

#### Scenario: Button not injected on non-HTML responses
- **WHEN** a user opens `/s/abc123/style.css` which serves a CSS file
- **THEN** no floating button SHALL be injected

#### Scenario: Button not injected on non-preview pages
- **WHEN** a user opens the home page `/`
- **THEN** no floating button SHALL be injected

### Requirement: Drawer open on button click
When the floating button is clicked, a drawer panel SHALL slide in from the right side of the viewport. The floating button SHALL be hidden while the drawer is open.

#### Scenario: Open drawer
- **WHEN** user clicks the floating button
- **THEN** a drawer panel SHALL slide in from the right, and the floating button SHALL disappear

#### Scenario: Close drawer with close button
- **WHEN** user clicks the close button (✕) in the drawer header
- **THEN** the drawer SHALL slide out, and the floating button SHALL reappear

#### Scenario: Drawer width
- **WHEN** the drawer is open
- **THEN** it SHALL occupy 320px width on the right side of the viewport, overlaying the content (not pushing it)

### Requirement: Drawer lazy data loading
The drawer SHALL fetch peer data from `GET /api/sessions/{id}/peers` only when the floating button is clicked (not on page load). A loading indicator SHALL be shown while data is being fetched.

#### Scenario: Fetch on first click
- **WHEN** user clicks the floating button for the first time
- **THEN** the system SHALL fetch data from `/api/sessions/{current-session-id}/peers` and show a loading indicator until the response arrives

#### Scenario: Subsequent opens reuse data
- **WHEN** user closes and reopens the drawer
- **THEN** the system SHALL NOT re-fetch data; it SHALL display the previously fetched data

#### Scenario: Fetch error handling
- **WHEN** the fetch request fails
- **THEN** the drawer SHALL display an error message with a retry button

### Requirement: Drawer content — peers by category
The drawer SHALL display a "Same Category" section listing documents from `byCategory` in the API response. Each entry SHALL show the document name as a clickable link that navigates to its preview page.

#### Scenario: Peers displayed
- **WHEN** the drawer is opened and `byCategory` contains entries
- **THEN** each entry SHALL show the document name linking to `/s/{sessionId}/`

#### Scenario: Empty category peers
- **WHEN** `byCategory` is empty
- **THEN** the "Same Category" section SHALL display "No documents in this category" or equivalent empty state

### Requirement: Drawer content — peers by project
The drawer SHALL display a "Same Project" section listing documents from `byProject` in the API response. Each entry SHALL show the document name as a clickable link.

#### Scenario: Peers displayed
- **WHEN** the drawer is opened and `byProject` contains entries
- **THEN** each entry SHALL show the document name linking to `/s/{sessionId}/`

#### Scenario: Empty project peers
- **WHEN** `byProject` is empty
- **THEN** the "Same Project" section SHALL display "No documents in this project" or equivalent empty state

### Requirement: Drawer content — home page link
The drawer SHALL include a "Back to Home" link that navigates to `/`.

#### Scenario: Home link present
- **WHEN** the drawer is opened
- **THEN** a "Back to Home" link SHALL be visible and navigate to `/` when clicked

### Requirement: CSS isolation
All injected CSS SHALL use high-specificity selectors (prefixed with `#sth-drawer`) to minimize conflicts with the user's HTML content. Critical layout properties SHALL use `!important`.

#### Scenario: No style conflict
- **WHEN** the user's HTML page defines styles that could conflict with the drawer
- **THEN** the drawer SHALL render correctly without visual degradation

### Requirement: Graceful degradation
If JavaScript fails or the API is unavailable, the drawer SHALL NOT break the user's HTML content rendering. The floating button and drawer are purely additive overlays.

#### Scenario: JS disabled
- **WHEN** JavaScript is not available in the browser
- **THEN** the user's HTML content SHALL render normally without any visual artifacts
