## Search — Last Downloaded timestamp

SearchPage now shows when an artifact was last downloaded:

- Main component row: small `↓ <date>` line under "Modified" date (only when the artifact has been downloaded at least once)
- Expanded asset rows: `↓ <date>` appended inline after `lastModified`
- Falls back to component-level `lastDownloaded` when asset-level is absent
- No backend changes — API already returned the field; frontend types updated to expose it
