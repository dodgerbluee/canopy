# AUTH05 executed verification

Removed Your name from first registration. Setup sends only username/password
and garden preferences; the backend defaults an omitted display name to the
normalized username. Existing user names/credentials remain unchanged.

The account password minimum is ten characters, consistently used by setup,
admin creation/reset and password-change UI plus the Users backend service.
Frontend/server character counting agrees for non-BMP Unicode characters.
Existing maximum byte limits remain in force.

Executed backend identity and HTTP suites against isolated PostgreSQL schemas:
pass, including nine/ten-character and Unicode boundaries, setup without a name
and successful login. All 493 Angular tests pass, including a registration
boundary test that verifies no displayName is sent. Production combined image
build passes. Refreshed the local application with matched API/worker binaries,
without resetting data. Actual image smoke processed a queued photo, preserved
capture date/private preview, and recovered from worker failure.

Read-only desktop/light and phone/dark browser checks confirm exactly three
registration fields, no name field, nine-character rejection and ten-character
acceptance, correct theme and no overflow. Axe reports zero violations. No
registration was submitted in the preview. Plan, boundary and whitespace checks
pass. Detailed ignored logs/screenshots are beside this summary.
Preview: http://localhost:7081/setup.
