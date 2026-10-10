# Private SQLite storage

Startup requires the selected storage directory to belong to the runtime UID with no group/other permissions. Database, WAL, SHM and rollback journal files must be owned regular files with no group/other permissions and no additional hard links. New directories and files use 0700 and 0600. macOS extended ACLs are rejected. Persistent SQLite requires POSIX ownership support; PostgreSQL selection bypasses these filesystem checks.

Existing unsafe storage is refused before SQLite opens it; startup does not silently chmod existing files or unrelated parents. Keep the selected directory under an operator-controlled path. Administrators and processes running as the same UID remain trusted.

1. Stop the service. Retain a private operational backup and verify which path and runtime UID the deployment uses.
2. Use a dedicated storage directory owned by that UID. Repair only that directory and its database/WAL/SHM/journal files to 0700/0600; remove macOS extended ACLs and replace links with regular private files. Do not recursively chmod shared parents. Existing Docker volumes may need their `/data` directory repaired; the supplied image initializes new volumes privately.
3. Start the service; verify `/ready`, authenticated retrieval and an idempotent capture retry. Startup refuses unsupported database schemas independently of permissions.
4. Create an administrator backup in a new file within a private owned directory. Restore into an empty private volume, preserving runtime ownership and 0600 file permissions.

Verify using synthetic isolated data:

```sh
fnm exec --using=24.21.0 npm run test -w @scratchpad/web -- src/server/sqlite-permissions.test.ts src/server/readiness.test.ts src/server/imports.test.ts
fnm exec --using=24.21.0 npm run lint
fnm exec --using=24.21.0 npm run typecheck
fnm exec --using=24.21.0 npm test
fnm exec --using=24.21.0 npm run build
```

Canonical Linux CI additionally runs the filesystem probe as `nobody`: a public synthetic control must be readable while live database/companions and backup are denied. macOS checks an actual extended ACL. These checks establish local storage boundaries, not deployed acceptance or evidence of compromise.
