# M2 collection listening upgrade

Migration 0009 adds the listening-source projection. It does not remove or rewrite
catalog, media, library roots, queue, playlist, favorite or listening-session data.
Existing grouping backfill refreshes its own completion timestamp as before.

Before updating a working server:

1. Preserve a full PostgreSQL backup and verify a restore in a separate database.
2. Verify the restored nonempty database with the new executable's
   `-migrate-only` mode and compare existing source/user records and the legacy
   migration ledger. Preserve the original backup and previous executable.
3. Obtain approval for the live update/service interruption, then stop the old
   listener. Keep the existing private `RESONANCE_DATABASE_URL` configuration.
4. Run the new executable with `-migrate-only`. A failed migration/readiness check
   is not success; inspect the error and retained backup before restarting.
5. Restart with the same trusted-LAN listener configuration and loopback-only
   Host listener. Check `/ready`, catalog/queue/playlist reads and Range playback.
6. Let the PWA update settle through its normal lifecycle. Close old Resonance
   tabs when ready to use the new shell; do not force activation over audio.

The executed M2 campaign restored the owner's v8 backup in isolation, compared
18 existing tables and the v1–v8 ledger, and verified the v9 contract. Its main
promotion was initially blocked by automatic review, then explicitly approved by
the owner and executed with data/ledger equality and readiness checks. Read-only
main-library UI/Range verification preserved the queue. See
[verification](../benchmarks/M2-collections-verification.json).

Migration 0009 is forward-only. To return to the prior application, use the
retained backup in a separate restored v8 database with the prior executable;
the old binary must not be pointed at a v9 schema. Preserve the v9 database and
any new listener-owned state while assessing recovery. Do not delete the current
database or music files to work around a failure. Restoring a database does not
prove that another host has the original media or root identity.
