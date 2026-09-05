# Fixed historical SQLite schemas

These SQL fixtures were dumped from an empty database opened and closed by
`store.NewSQLiteStore` at the following release commits:

| Fixture | Source commit |
| --- | --- |
| M0 | `4214ef355b1018bec6c3f89451ed22290787f0b5` |
| M1 | `b805726c3b33ee7d917a44147010f513b05e0fd4` |
| M2 | `8d8eca006a7aaf86c5d0bb1f12b707fa5e871eb7` |

Each constructor was executed in an isolated checkout of its source commit.
The dump preserves the `sql` entries from `sqlite_schema`, excluding SQLite's
internal objects, with tables before indexes (`ORDER BY type DESC, name`).
SQLite recreates internal sequence/autoindex objects when applying the DDL.
All three historical releases used `user_version = 0`.

Do not regenerate these fixtures with current migrations or add current columns.
Tests insert explicit historical rows after loading the fixed DDL. Keep schema
and data choices separate so a new production helper cannot hide an upgrade bug.
