## ADDED Requirements

### Requirement: Delete button on session list items
Each session listed on the homepage SHALL display a small delete button. The button SHALL be positioned after the session link and timestamp within each list item.

#### Scenario: Session list item shows delete button
- **WHEN** sessions are displayed on the homepage
- **THEN** each session list item SHALL contain a delete button

### Requirement: Delete confirmation prompt
The system SHALL prompt the user for confirmation before sending a delete request. The prompt SHALL display the session name.

#### Scenario: User clicks delete button
- **WHEN** the user clicks the delete button on a session
- **THEN** a confirmation dialog SHALL appear asking the user to confirm deletion

#### Scenario: User cancels deletion
- **WHEN** the user dismisses the confirmation dialog without confirming
- **THEN** no delete request SHALL be sent and the session SHALL remain visible

### Requirement: Delete request to API
Upon user confirmation, the system SHALL send a `DELETE /api/sessions/:id` request to the server using the session ID from the button's `data-session-id` attribute.

#### Scenario: Successful deletion
- **WHEN** the user confirms deletion and the API returns HTTP 200
- **THEN** the session list item SHALL be removed from the page DOM

#### Scenario: Deletion fails with 404
- **WHEN** the user confirms deletion and the API returns HTTP 404
- **THEN** an error message SHALL be shown to the user and the list item SHALL remain visible

#### Scenario: Network error during deletion
- **WHEN** the user confirms deletion and the fetch request fails due to a network error
- **THEN** an error message SHALL be shown to the user and the list item SHALL remain visible

### Requirement: Delete button styling
The delete button SHALL be styled as a small, subtle button that becomes visually prominent on hover. The button SHALL use text (e.g., "×" or "Delete") rather than an icon image.

#### Scenario: Default button appearance
- **WHEN** the delete button is displayed
- **THEN** it SHALL appear as small, low-contrast text matching the page's design language

#### Scenario: Hover state
- **WHEN** the user hovers over the delete button
- **THEN** the button SHALL change to a red or visually distinct color to indicate a destructive action
