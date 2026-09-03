# Storage → Directory → User Permission Model: Review & Discussion

> Last updated: 2026-09-03
>
> Purpose: lay out the current state, fixed items, core difficulties, and open decisions of the "user ↔ shared folder ↔ protocol" permission chain, so the team can discuss and decide on the path forward.

---

## 1. What we ultimately want

In one sentence: for every user, on every shared folder, over every protocol, precisely control "can they access it, and read-only or read-write".

The ideal model is a three-dimensional matrix:

```
user × folder × protocol → access level (deny / read-only / read-write)
```

User management already has a "permission matrix" page whose UI is designed around exactly this three-way model — each cell offers read-write / read-only / deny. But neither the backend data model nor the protocols themselves live up to this UI. That mismatch is the root of all the problems below.

---

## 2. Current state (data model + what each protocol can actually do)

### Data model

The `folders` table in `folders.db` is **folder-level**:

```
each folder = one valid_users whitelist + one permission (read-write / read-only / deny)
```

Note: `permission` is "one value per folder", not "one value per user". So the data model itself cannot express a user-level difference like "A read-write, B read-only".

### What each protocol can actually enforce

| Protocol | Per-user whitelist | Per-user read/write | Notes |
|----------|:---:|:---:|-------|
| SMB | ✅ | ❌ | The only protocol with a per-user whitelist. `valid users` controls who can connect; `read only` is a share-level switch (the whole group goes read-only together, can't split per user). We just added `force user/group` so whitelisted users can actually write into the directory. |
| NFS | ❌ | ❌ | Subnet-level export, no per-user concept (inherent to the protocol, can't be changed). |
| FTP | ❌ | ❌ | vsftpd chroots users to `/data/private/$USER`; shared folders are not exposed over FTP at all; userlist is whitelist mode. |
| WebDAV | ❌ | ❌ | `rclone serve webdav /data` with a single global credential (fm), ignores per-folder/per-user permissions. |
| S3 | ❌ | ❌ | Same — `rclone serve s3 /data` with a single global auth-key. |

**Conclusion**: only SMB has any real "per-user × per-folder" isolation today, and even SMB only covers the "who can access" dimension — read/write granularity is still share-level.

---

## 3. Fixed items that now run correctly (deployed)

1. Creating a user never actually enabled Samba — now it runs `smbpasswd -a` with the password.
2. SMB managed shares lacked `force user/group` + `create mask`, so whitelisted users couldn't write into the directory — now aligned with the template.
3. NFS hardcoded the `192.168.0.0/16` subnet — now auto-detects the LAN subnet.
4. FTP userlist whitelist logic was inverted (enable did remove, disable did add) — now fixed to whitelist semantics.
5. Duplicate `START` marker in the smb.conf managed section — `replaceManagedBlock` is now idempotent.
6. The permission matrix edited smb.conf directly, bypassing metadata and getting clobbered by the next config sync — now it goes through metadata + unified regeneration.

These six fix "it runs and isn't silently overwritten" bugs, but do not address the core "per-user granularity" problem.

---

## 4. Core difficulties

### Difficulty 1: The data model is folder-level, not user-level
A folder has a single `permission`. To support "A read-write, B read-only", metadata must move from "folder-level permission" to a "user × folder permission table" — a schema-level refactor that ripples through the frontend matrix, the backend generation engine, and every protocol's enforcement.

### Difficulty 2: SMB per-user read-only requires `read list` / `write list`
Samba natively supports `read list` / `write list` to split read/write per user — the only path that doesn't require swapping services. But to wire it up, the metadata model from Difficulty 1 must be built first; the two are coupled.

### Difficulty 3: FTP has no solution under vsftpd
Whether real or virtual users, vsftpd is "one user = one chroot root" — it cannot express "one user accessing multiple folders", and `write` permission is per-user, not per-directory. A real matrix requires switching to proftpd (mod_auth_file) or pure-ftpd — a service swap, not a config addition.

### Difficulty 4: WebDAV / S3 are global services
`rclone serve` exposes the entire `/data` with a single credential. Per-folder isolation requires either a separate instance per folder (depends on the "multi-instance port config" TODO) or a service that supports ACLs.

### Difficulty 5: File ownership consistency
SMB now uses `force user = nasUser`, so files are owned by nasUser. If FTP later writes as virtual users or WebDAV as a different instance user, ownership splits across multiple IDs — we need a unified ownership policy, otherwise SMB-written files and FTP-written files in the same folder fight over permissions.

### Difficulty 6: The UI badges over-claim coverage
`isFTPAccessible` and friends only check "is the service running", then stamp every folder with FTP✓ / DAV✓ / S3✓ even when that folder is unreachable. They should either reflect real reachability or be explicitly labeled "global service", otherwise users are misled into thinking per-user isolation already exists.

---

## 5. Open decisions for discussion

1. **Scope**: Does a home NAS actually need per-user read/write granularity? Or is "who can access + one read/write level per folder" enough? This decides whether Difficulties 1 & 2 are worth doing. If not, the matrix UI's "read-only" option should be cut or downgraded to avoid misleading users.

2. **Phasing**: Is it acceptable to do "SMB per-user first, FTP/WebDAV/S3 explicitly labeled global access"? Or must all protocols align at once?

3. **FTP direction**: keep vsftpd (private dir only), add virtual users (one user = one dir), or switch to proftpd for a true matrix?

4. **Metadata refactor scope**: a new "user × folder" permission table, or add `read_list`/`write_list` fields to the `folders` table?

---

## Related TODOs

- #30 Unified user-directory permission model (per-user × per-folder × per-protocol)
- #29 Multi-instance service port config (WebDAV / S3 / Web)
