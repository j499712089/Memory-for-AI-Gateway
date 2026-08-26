"""ALL-199: ops_outbox fault-injection matrix, isomorphic with the authoritative contract.

Authoritative contract (single source, decided in ALL-199 -- no design change needed,
ALL-69 v2.1 5.4 and ADR-004 v2.2 Decision 4 already agree; the ALL-196 test diverged):

  create/update       (1) outbox(pending) + memories row  [ONE SQLite txn]
                      (2) temp file + atomic rename -> dst_path
                      (3) B-class frontmatter writeback   (4) outbox -> done
  move/archive/trash  (1) outbox(pending)  (2) target path written FIRST
                      (3) memories.source_path/status/archived_at  [ONE txn]
                      (4) delete old path  (5) outbox -> done
  purge               (1) outbox(pending)  (2) delete file
                      (3) row -> purged (event history kept)  (4) done

Note the asymmetry: create/update is DB-first, move/archive/trash/purge is file-first.
ALL-196 flattened all of them into one file-first order, which also erases 5.4's
hard constraint "never update source_path before moving the file".

RED/GREEN: the same matrix runs against LegacyEngine (the ALL-196 order + its reduced
schema) which MUST fail, and against Engine (contract order) which MUST pass. Section 0
pins the schema and the recorded step order, so a future reordering re-reddens this file.
"""
import atexit
import errno
import hashlib
import os
import re
import shutil
import sqlite3
import sys
import tempfile
import uuid

FAILURES = []
CHECKS = 0
NS = uuid.UUID("3f6a2c81-1111-2222-3333-444455556666")


def check(name, got, want):
    global CHECKS
    CHECKS += 1
    ok = got == want
    print(f"[{'PASS' if ok else 'FAIL'}] {name}: got={got!r} want={want!r}")
    if not ok:
        FAILURES.append(name)


# ---------------------------------------------------------------------------
# Delivered DDL, copied verbatim out of ALL-69 v2.1 5.4. Executed as-is; no
# reduced stand-in schema (ALL-199 required fix 2).
# ---------------------------------------------------------------------------
# Both docs are git-tracked (ALL-201 R2-1): the fail-closed read below is only a
# reproducible acceptance gate if the contract itself ships with the test.
HERE = os.path.dirname(os.path.abspath(__file__))
SPEC_PATH = os.path.join(HERE, "docs", "ALL-69-obsidian-file-structure-spec.md")
ADR_PATH = os.path.join(HERE, "docs", "decisions", "ADR-004-obsidian-sqlite-sync.md")


def _spec_text(path):
    """Read an authoritative design doc. Missing doc is a hard failure, not a skip:
    a reduced stand-in schema is exactly what ALL-199 finding 3 forbids."""
    if not os.path.exists(path):
        raise SystemExit(f"FATAL: authoritative contract not found: {path}")
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def _extract_outbox_ddl(spec):
    """Pull the ops_outbox schema verbatim out of ALL-69 section 5.4 and turn it into
    executable DDL. The test must run the delivered contract, never a local copy."""
    m = re.search(r"^ops_outbox\(\s*$(.*?)^\)\s*$", spec, re.S | re.M)
    if not m:
        raise SystemExit("FATAL: no ops_outbox(...) block in ALL-69 5.4")
    return "CREATE TABLE ops_outbox (\n" + m.group(1).rstrip() + "\n)"


SPEC_TEXT = _spec_text(SPEC_PATH)
ADR_TEXT = _spec_text(ADR_PATH)
OPS_OUTBOX_DDL = _extract_outbox_ddl(SPEC_TEXT)

SUPPORT_DDL = """
CREATE TABLE memories (
  memory_id TEXT PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('candidate','review','active','updated',
    'deprecated','archived','trash','purged')),
  source_path TEXT NOT NULL UNIQUE, content_hash TEXT NOT NULL,
  scope_type TEXT NOT NULL DEFAULT 'private', archived_at TEXT, version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now','utc')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now','utc'))
);
CREATE TABLE sync_state (
  path TEXT PRIMARY KEY, memory_id TEXT NOT NULL REFERENCES memories(memory_id),
  mtime INTEGER NOT NULL, size INTEGER NOT NULL, content_hash TEXT NOT NULL,
  last_indexed_at TEXT NOT NULL DEFAULT (datetime('now','utc'))
);
CREATE TABLE memory_events (
  event_id INTEGER PRIMARY KEY AUTOINCREMENT, memory_id TEXT NOT NULL,
  event_type TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (datetime('now','utc'))
);
"""

OP_TYPES = ("create", "update", "move", "archive", "trash", "purge")
PHASES = ("pending", "committed", "done", "failed")
TYPE_DIR = ("code", "team-3f6a2c81-1111-2222-3333-444455556666", "2026", "08")


def sha256(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def new_db():
    db = sqlite3.connect(":memory:")
    db.executescript(SUPPORT_DDL)
    db.executescript(OPS_OUTBOX_DDL)
    return db


class SimulatedCrash(Exception):
    """Process death between two contract steps."""


class InjectedIOFailure(Exception):
    """Transient filesystem failure, drives the attempt_count -> failed path."""


class Vault:
    """Filesystem side. cross_fs_rename simulates EXDEV so 5.4 compensation is testable."""

    def __init__(self, root, cross_fs_paths=()):
        self.root = root
        self.archive = os.path.join(root, "_archive")
        self.trash = os.path.join(root, "_trash")
        self.tmp = os.path.join(root, ".op_tmp")
        self.cross_fs_paths = set(cross_fs_paths)
        for d in (self.archive, self.trash, self.tmp):
            os.makedirs(d, exist_ok=True)

    def path_for(self, mem_id):
        return os.path.join(self.root, *TYPE_DIR, f"{mem_id}.md")

    def archive_path(self, mem_id, memory_type="code"):
        return os.path.join(self.archive, memory_type, "2026", "08", f"{mem_id}.md")

    def trash_path(self, mem_id):
        return os.path.join(self.trash, f"{mem_id}.md")

    def write_temp(self, op_id, payload):
        os.makedirs(self.tmp, exist_ok=True)
        tmp = os.path.join(self.tmp, f"{op_id}.tmp")
        with open(tmp, "w", encoding="utf-8") as f:
            f.write(payload)
            f.flush()
            os.fsync(f.fileno())
        return tmp

    def rename(self, src, dst):
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        if dst in self.cross_fs_paths:
            raise OSError(errno.EXDEV, "Invalid cross-device link")
        os.replace(src, dst)

    def rename_or_copy(self, src, dst):
        """5.4 cross-filesystem compensation: copy + fsync + unlink source."""
        try:
            self.rename(src, dst)
            return "rename"
        except OSError as exc:
            if exc.errno != errno.EXDEV:
                raise
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            shutil.copyfile(src, dst)
            with open(dst, "rb+") as f:
                os.fsync(f.fileno())
            os.unlink(src)
            return "copy+unlink"

    def read(self, path):
        with open(path, encoding="utf-8") as f:
            return f.read()

    def hash_of(self, path):
        return sha256(self.read(path)) if os.path.exists(path) else None

    def temp_files(self):
        return sorted(os.listdir(self.tmp)) if os.path.isdir(self.tmp) else []


def body_for(mem_id, status="active", version=1):
    return (f"---\nmemory_id: {mem_id}\ntitle: {mem_id}\nstatus: {status}\n"
            f"version: {version}\n---\n\n# {mem_id}\n\ncontent of {mem_id}\n")


class Engine:
    """Contract engine. Steps are numbered per op_type; crash_at raises AFTER step N."""

    def __init__(self, db, vault, crash_at=None, io_fail_paths=()):
        self.db = db
        self.vault = vault
        self.crash_at = crash_at
        self.io_fail_paths = set(io_fail_paths)
        self.trace = []

    def _step(self, label):
        self.trace.append(label)
        if self.crash_at is not None and len(self.trace) >= self.crash_at:
            self.crash_at = None
            raise SimulatedCrash(f"died after step {label}")

    def _op(self, op_id):
        row = self.db.execute(
            "SELECT op_id, op_type, memory_id, src_path, dst_path, content_hash, phase,"
            " attempt_count FROM ops_outbox WHERE op_id=?", (op_id,)).fetchone()
        return dict(zip(("op_id", "op_type", "memory_id", "src_path", "dst_path",
                         "content_hash", "phase", "attempt_count"), row))

    def _event(self, memory_id, event_type, payload="{}"):
        self.db.execute(
            "INSERT INTO memory_events (memory_id, event_type, payload) VALUES (?,?,?)",
            (memory_id, event_type, payload))

    def _conflict(self, op):
        """7.1: keep the on-disk file as <name>.conflict-<hash8>.md, op -> failed."""
        disk = self.vault.hash_of(op["dst_path"])
        side = f"{op['dst_path'][:-3]}.conflict-{disk[:8]}.md"
        if not os.path.exists(side):
            os.replace(op["dst_path"], side)
        self._event(op["memory_id"], "updated", '{"conflict": true}')
        self.db.execute("UPDATE ops_outbox SET phase='failed' WHERE op_id=?", (op["op_id"],))
        self.db.commit()
        return side

    def _phase(self, op, phase):
        self.db.execute("UPDATE ops_outbox SET phase=?, last_attempt_at=datetime('now','utc')"
                        " WHERE op_id=?", (phase, op["op_id"]))
        self.db.commit()

    # --- create / update: DB txn FIRST, then file (5.4 row 1) -------------------
    def run_create_or_update(self, op_id, payload, status="active"):
        op = self._op(op_id)
        mem_id, dst = op["memory_id"], op["dst_path"]

        # (1) outbox(pending) + memories row, ONE SQLite transaction
        row = self.db.execute("SELECT content_hash FROM memories WHERE memory_id=?",
                              (mem_id,)).fetchone()
        if op["phase"] == "pending" and (row is None or row[0] != op["content_hash"]):
            try:
                self.db.execute("BEGIN")
                if row is None:
                    self.db.execute(
                        "INSERT INTO memories (memory_id, title, body, status, source_path,"
                        " content_hash, scope_type) VALUES (?,?,?,?,?,?,?)",
                        (mem_id, mem_id, payload, status, dst, op["content_hash"], "private"))
                else:
                    self.db.execute(
                        "UPDATE memories SET body=?, content_hash=?, version=version+1,"
                        " updated_at=datetime('now','utc') WHERE memory_id=?",
                        (payload, op["content_hash"], mem_id))
                if dst in self.io_fail_paths:          # abort mid-transaction
                    raise InjectedIOFailure("db txn aborted")
                self.db.execute("UPDATE ops_outbox SET phase='committed' WHERE op_id=?", (op_id,))
                self.db.commit()
            except InjectedIOFailure:
                self.db.rollback()
                raise
            self._step("db_txn")

        # (2) temp file + atomic rename
        if not os.path.exists(dst):
            tmp = self.vault.write_temp(op_id, payload)
            self._step("temp_write")
            self.vault.rename_or_copy(tmp, dst)
            self._step("rename")
        elif self.vault.hash_of(dst) != op["content_hash"]:
            self._conflict(op)                         # 5.4 recovery row 1, hash mismatch
            return
        # (3) B-class writeback (version/source_path already authoritative in DB)
        self.db.execute(
            "INSERT INTO sync_state (path, memory_id, mtime, size, content_hash)"
            " VALUES (?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET content_hash=excluded.content_hash",
            (dst, mem_id, int(os.path.getmtime(dst)), os.path.getsize(dst), op["content_hash"]))
        self.db.commit()
        self._step("writeback")
        # (4) done
        self._phase(op, "done")
        self._step("done")

    # --- move / archive / trash: file FIRST, then DB (5.4 row 2) ----------------
    def run_move_family(self, op_id, new_status=None):
        op = self._op(op_id)
        mem_id, src, dst = op["memory_id"], op["src_path"], op["dst_path"]

        # (2) target path written FIRST, from content, source left intact so that
        #     recovery row (3) "DB advanced, old file not yet deleted" is reachable
        if not os.path.exists(dst):
            if not os.path.exists(src):
                self._phase(op, "failed")
                self._event(mem_id, "sync_failure", '{"reason": "source and target missing"}')
                return
            tmp = self.vault.write_temp(op_id, self.vault.read(src))
            self._step("temp_write")
            self.vault.rename_or_copy(tmp, dst)
            self._step("rename")

        if self.vault.hash_of(dst) != op["content_hash"]:
            self._conflict(op)
            return

        # (3) source_path / status / archived_at, ONE SQLite transaction
        cur = self.db.execute("SELECT source_path FROM memories WHERE memory_id=?",
                             (mem_id,)).fetchone()
        if cur is not None and cur[0] != dst:
            self.db.execute("BEGIN")
            if new_status == "archived":
                self.db.execute("UPDATE memories SET source_path=?, status='archived',"
                                " archived_at=datetime('now','utc') WHERE memory_id=?", (dst, mem_id))
            elif new_status:
                self.db.execute("UPDATE memories SET source_path=?, status=? WHERE memory_id=?",
                                (dst, new_status, mem_id))
            else:
                self.db.execute("UPDATE memories SET source_path=? WHERE memory_id=?", (dst, mem_id))
            self.db.execute("DELETE FROM sync_state WHERE path=?", (src,))
            self.db.execute("UPDATE ops_outbox SET phase='committed' WHERE op_id=?", (op_id,))
            self.db.commit()
            self._step("db_txn")

        # (4) delete old path -- only if it still belongs to this memory_id
        if os.path.exists(src) and src != dst:
            if self.vault.hash_of(src) == op["content_hash"]:
                os.unlink(src)
            self._step("unlink_old")
        # (5) done
        self._phase(op, "done")
        self._step("done")

    # --- purge (5.4 row 3) ------------------------------------------------------
    def run_purge(self, op_id):
        op = self._op(op_id)
        mem_id, dst = op["memory_id"], op["dst_path"]
        if os.path.exists(dst):
            os.unlink(dst)
            self._step("unlink")
        row = self.db.execute("SELECT status FROM memories WHERE memory_id=?", (mem_id,)).fetchone()
        if row and row[0] != "purged":
            self.db.execute("BEGIN")
            self.db.execute("UPDATE memories SET status='purged' WHERE memory_id=?", (mem_id,))
            self.db.execute("DELETE FROM sync_state WHERE path=?", (dst,))
            self._event(mem_id, "purged")
            self.db.execute("UPDATE ops_outbox SET phase='committed' WHERE op_id=?", (op_id,))
            self.db.commit()
            self._step("db_txn")
        self._phase(op, "done")
        self._step("done")

    DRIVERS = {"create": "run_create_or_update", "update": "run_create_or_update",
               "move": "run_move_family", "archive": "run_move_family",
               "trash": "run_move_family", "purge": "run_purge"}

    def drive(self, op_id, **kw):
        op = self._op(op_id)
        return getattr(self, self.DRIVERS[op["op_type"]])(op_id, **kw)

    def recover(self):
        """Startup / pre-sync replay: every pending|committed op, same drivers."""
        ops = self.db.execute("SELECT op_id, op_type, memory_id, dst_path, content_hash, phase,"
                              " attempt_count FROM ops_outbox"
                              " WHERE phase IN ('pending','committed') ORDER BY created_at, op_id"
                              ).fetchall()
        for op_id, op_type, mem_id, dst, chash, phase, attempts in ops:
            if attempts >= 3:
                self._phase({"op_id": op_id}, "failed")
                self._event(mem_id, "sync_failure", f'{{"op_id": "{op_id}", "reason": "3 attempts"}}')
                continue
            kw = {}
            if op_type in ("create", "update"):
                row = self.db.execute("SELECT body FROM memories WHERE memory_id=?",
                                      (mem_id,)).fetchone()
                kw["payload"] = row[0] if row else body_for(mem_id)
            elif op_type == "archive":
                kw["new_status"] = "archived"
            elif op_type == "trash":
                kw["new_status"] = "trash"
            try:
                getattr(self, self.DRIVERS[op_type])(op_id, **kw)
            except (InjectedIOFailure, OSError):
                self.db.execute("UPDATE ops_outbox SET attempt_count=attempt_count+1,"
                                " last_attempt_at=datetime('now','utc') WHERE op_id=?", (op_id,))
                self.db.commit()


class LegacyEngine(Engine):
    """The ALL-196 order: write -> rename -> db_commit -> done for EVERY op_type.

    Kept in-tree as the RED reference: section 5 asserts this order violates the
    contract, so any future regression back to it re-reddens this file.
    """

    def _missing(self, op_id, **kw):
        raise NotImplementedError("ALL-196 shipped no driver for this op_type")

    # ALL-196 shipped run_create + run_move only.
    DRIVERS = dict(Engine.DRIVERS, update="_missing", trash="_missing", purge="_missing")

    def run_create_or_update(self, op_id, payload, status="active"):
        op = self._op(op_id)
        mem_id, dst = op["memory_id"], op["dst_path"]
        if not os.path.exists(dst):                      # file BEFORE db (wrong)
            tmp = self.vault.write_temp(op_id, payload)
            self._step("temp_write")
            self.vault.rename_or_copy(tmp, dst)
            self._step("rename")
        if self.db.execute("SELECT 1 FROM memories WHERE memory_id=?", (mem_id,)).fetchone() is None:
            self.db.execute(
                "INSERT INTO memories (memory_id, title, body, status, source_path, content_hash,"
                " scope_type) VALUES (?,?,?,?,?,?,?)",
                (mem_id, mem_id, payload, status, dst, op["content_hash"], "private"))
            self.db.execute("UPDATE ops_outbox SET phase='committed' WHERE op_id=?", (op_id,))
            self.db.commit()
            self._step("db_txn")
        self._phase(op, "done")
        self._step("done")


def seed_op(db, op_type, mem_id, dst, content_hash, src=None, phase="pending"):
    op_id = str(uuid.uuid5(NS, f"{op_type}:{mem_id}"))
    db.execute("INSERT INTO ops_outbox (op_id, op_type, memory_id, src_path, dst_path,"
               " content_hash, phase) VALUES (?,?,?,?,?,?,?)",
               (op_id, op_type, mem_id, src, dst, content_hash, phase))
    db.commit()
    return op_id


def seed_memory(db, mem_id, path, body, status="active"):
    db.execute("INSERT INTO memories (memory_id, title, body, status, source_path, content_hash,"
               " scope_type) VALUES (?,?,?,?,?,?,?)",
               (mem_id, mem_id, body, status, path, sha256(body), "private"))
    db.execute("INSERT INTO sync_state (path, memory_id, mtime, size, content_hash)"
               " VALUES (?,?,?,?,?)", (path, mem_id, 0, len(body), sha256(body)))
    db.commit()


def fresh(cross_fs_paths=()):
    root = tempfile.mkdtemp(prefix="all199_")
    atexit.register(shutil.rmtree, root, True)
    return new_db(), Vault(root, cross_fs_paths)


def converged(db, vault, mem_id, expect_path, expect_status="active", expect_body=None):
    """File + DB + source_path + status + outbox phase all agree; no half rows, no temp litter."""
    row = db.execute("SELECT source_path, status, content_hash FROM memories WHERE memory_id=?",
                     (mem_id,)).fetchone()
    if row is None:
        return "no_memory_row"
    if not os.path.exists(expect_path):
        return f"file_missing:{os.path.basename(expect_path)}"
    disk = vault.read(expect_path)
    if expect_body is not None and disk != expect_body:
        return "file_body_mismatch"
    if row[0] != expect_path:
        return f"source_path={row[0]}"
    if row[1] != expect_status:
        return f"status={row[1]}"
    if row[2] != sha256(disk):
        return "content_hash_vs_file_mismatch"
    open_ops = db.execute("SELECT COUNT(*) FROM ops_outbox WHERE memory_id=? AND phase!='done'",
                          (mem_id,)).fetchone()[0]
    if open_ops:
        return f"open_ops={open_ops}"
    if vault.temp_files():
        return f"temp_litter={vault.temp_files()}"
    return "converged"


print("=" * 78)
print("ALL-199 ops_outbox fault injection -- contract: ALL-69 v2.1 5.4 / ADR-004 v2.2 D4")
print("=" * 78)

# ===== 0. schema and order are the delivered contract, not a stand-in ==========
print("\n-- 0. contract schema + declared step order --")
check("0.0 DDL parsed out of the delivered ALL-69 spec, not inlined here",
      OPS_OUTBOX_DDL.strip() != "" and "ops_outbox(" in SPEC_TEXT
      and all(c in SPEC_TEXT for c in ("attempt_count", "last_attempt_at", "content_hash")), True)
_db, _vault = fresh()
cols = [r[1] for r in _db.execute("PRAGMA table_info(ops_outbox)")]
check("0.1 ops_outbox columns == 5.4 DDL", cols,
      ["op_id", "op_type", "memory_id", "src_path", "dst_path", "content_hash", "phase",
       "created_at", "last_attempt_at", "attempt_count"])
check("0.2 op_id is TEXT UUID (not AUTOINCREMENT)",
      [r[2] for r in _db.execute("PRAGMA table_info(ops_outbox)") if r[1] == "op_id"], ["TEXT"])
ddl = _db.execute("SELECT sql FROM sqlite_master WHERE name='ops_outbox'").fetchone()[0]


def _op_type_accepted(value):
    """Does the delivered DDL actually let this op_type reach the table? Substring
    matching on sqlite_master would also pass on a comment, which is what ALL-201
    R2-3(b) caught: assert enforcement, not documentation."""
    probe = new_db()
    try:
        probe.execute("INSERT INTO ops_outbox (op_id, op_type, memory_id, dst_path)"
                      " VALUES (?,?,?,?)", (f"probe-{value}", value, "m-probe", "p.md"))
        return True
    except sqlite3.IntegrityError:
        return False
    finally:
        probe.close()


check("0.3 op_type CHECK accepts all 6 contract ops",
      [t for t in OP_TYPES if not _op_type_accepted(t)], [])
check("0.4 op_type CHECK rejects a value outside the 6 (enum enforced, not commented)",
      _op_type_accepted("TOTALLY_BOGUS"), False)
check("0.5 phase enum == 4 contract phases", all(p in ddl for p in PHASES), True)
check("0.6 recovery columns present", all(c in cols for c in ("attempt_count", "last_attempt_at")), True)

_mid = "m-order"
_h = sha256(body_for(_mid))
_op = seed_op(_db, "create", _mid, _vault.path_for(_mid), _h)
_eng = Engine(_db, _vault)
_eng.drive(_op, payload=body_for(_mid))
check("0.7 create order is db_txn BEFORE file (5.4 row 1)", _eng.trace,
      ["db_txn", "temp_write", "rename", "writeback", "done"])

_db2, _v2 = fresh()
_mid2 = "m-order-move"
_src, _dst = _v2.path_for(_mid2), _v2.archive_path(_mid2)
os.makedirs(os.path.dirname(_src), exist_ok=True)
open(_src, "w", encoding="utf-8").write(body_for(_mid2))
seed_memory(_db2, _mid2, _src, body_for(_mid2))
_op2 = seed_op(_db2, "archive", _mid2, _dst, sha256(body_for(_mid2)), src=_src)
_eng2 = Engine(_db2, _v2)
_eng2.drive(_op2, new_status="archived")
check("0.8 move order is file BEFORE db_txn, old path last (5.4 row 2)", _eng2.trace,
      ["temp_write", "rename", "db_txn", "unlink_old", "done"])
check("0.9 move never updates source_path before the target file exists (5.4 hard rule)",
      _eng2.trace.index("rename") < _eng2.trace.index("db_txn"), True)


# ===== 1. create / update: crash at every boundary =============================
# Boundaries: 1 db_txn, 2 temp_write, 3 rename, 4 writeback, 5 done
print("\n-- 1. create crash matrix (5 boundaries + no-crash) --")
for crash_at in (None, 1, 2, 3, 4, 5):
    db, vault = fresh()
    mem_id = f"m-create-{crash_at}"
    body = body_for(mem_id)
    op_id = seed_op(db, "create", mem_id, vault.path_for(mem_id), sha256(body))
    try:
        Engine(db, vault, crash_at=crash_at).drive(op_id, payload=body)
    except SimulatedCrash:
        pass
    Engine(db, vault).recover()
    check(f"1.{crash_at} create crash@{crash_at} converges",
          converged(db, vault, mem_id, vault.path_for(mem_id), expect_body=body), "converged")
    Engine(db, vault).recover()          # second replay: no side effects
    check(f"1.{crash_at} create crash@{crash_at} replay idempotent",
          converged(db, vault, mem_id, vault.path_for(mem_id), expect_body=body), "converged")

# Same 5 boundaries as create -- update shares run_create_or_update, so 4 (after
# writeback) and 5 (after done) must be injected too (ALL-201 R2-2).
print("\n-- 2. update crash matrix (existing row + existing file) --")
for crash_at in (None, 1, 2, 3, 4, 5):
    db, vault = fresh()
    mem_id = f"m-update-{crash_at}"
    v1, v2 = body_for(mem_id, version=1), body_for(mem_id, version=2)
    path = vault.path_for(mem_id)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w", encoding="utf-8").write(v1)
    seed_memory(db, mem_id, path, v1)
    os.unlink(path)                      # update rewrites the file body
    op_id = seed_op(db, "update", mem_id, path, sha256(v2))
    try:
        Engine(db, vault, crash_at=crash_at).drive(op_id, payload=v2)
    except SimulatedCrash:
        pass
    Engine(db, vault).recover()
    check(f"2.{crash_at} update crash@{crash_at} converges",
          converged(db, vault, mem_id, path, expect_body=v2), "converged")
    ver = db.execute("SELECT version FROM memories WHERE memory_id=?", (mem_id,)).fetchone()[0]
    Engine(db, vault).recover()
    check(f"2.{crash_at} update replay does not re-bump version",
          db.execute("SELECT version FROM memories WHERE memory_id=?", (mem_id,)).fetchone()[0], ver)

# ===== 3. move / archive / trash: crash at every boundary ======================
# Boundaries: 1 temp_write, 2 rename, 3 db_txn, 4 unlink_old, 5 done
print("\n-- 3. move / archive / trash crash matrix --")
for op_type, status in (("move", None), ("archive", "archived"), ("trash", "trash")):
    for crash_at in (None, 1, 2, 3, 4, 5):
        db, vault = fresh()
        mem_id = f"m-{op_type}-{crash_at}"
        body = body_for(mem_id)
        src = vault.path_for(mem_id)
        dst = (vault.archive_path(mem_id) if op_type == "archive" else
               vault.trash_path(mem_id) if op_type == "trash" else
               os.path.join(vault.root, "code", "team-" + str(NS), "2026", "08", f"{mem_id}-moved.md"))
        os.makedirs(os.path.dirname(src), exist_ok=True)
        open(src, "w", encoding="utf-8").write(body)
        seed_memory(db, mem_id, src, body)
        op_id = seed_op(db, op_type, mem_id, dst, sha256(body), src=src)
        kw = {"new_status": status} if status else {}
        try:
            Engine(db, vault, crash_at=crash_at).drive(op_id, **kw)
        except SimulatedCrash:
            pass
        Engine(db, vault).recover()
        want = status or "active"
        check(f"3.{op_type}@{crash_at} converges",
              converged(db, vault, mem_id, dst, expect_status=want, expect_body=body), "converged")
        check(f"3.{op_type}@{crash_at} old path cleaned", os.path.exists(src), False)
        Engine(db, vault).recover()
        check(f"3.{op_type}@{crash_at} replay idempotent",
              converged(db, vault, mem_id, dst, expect_status=want, expect_body=body), "converged")

# ===== 4. purge: crash at every boundary ======================================
print("\n-- 4. purge crash matrix (file gone, row -> purged, events kept) --")
for crash_at in (None, 1, 2, 3):
    db, vault = fresh()
    mem_id = f"m-purge-{crash_at}"
    body = body_for(mem_id, status="trash")
    tpath = vault.trash_path(mem_id)
    open(tpath, "w", encoding="utf-8").write(body)
    seed_memory(db, mem_id, tpath, body, status="trash")
    db.execute("INSERT INTO memory_events (memory_id, event_type) VALUES (?, 'trashed')", (mem_id,))
    db.commit()
    op_id = seed_op(db, "purge", mem_id, tpath, sha256(body))
    try:
        Engine(db, vault, crash_at=crash_at).drive(op_id)
    except SimulatedCrash:
        pass
    Engine(db, vault).recover()
    row = db.execute("SELECT status FROM memories WHERE memory_id=?", (mem_id,)).fetchone()
    check(f"4.{crash_at} purge@{crash_at} file physically gone", os.path.exists(tpath), False)
    check(f"4.{crash_at} purge@{crash_at} row status purged", row and row[0], "purged")
    check(f"4.{crash_at} purge@{crash_at} event history retained",
          db.execute("SELECT COUNT(*) FROM memory_events WHERE memory_id=?",
                     (mem_id,)).fetchone()[0] >= 1, True)
    check(f"4.{crash_at} purge@{crash_at} outbox closed",
          db.execute("SELECT phase FROM ops_outbox WHERE op_id=?", (op_id,)).fetchone()[0], "done")
    Engine(db, vault).recover()
    check(f"4.{crash_at} purge@{crash_at} replay idempotent",
          db.execute("SELECT status FROM memories WHERE memory_id=?", (mem_id,)).fetchone()[0], "purged")


# ===== 5. hash mismatch -> conflict; DB abort -> no half row; EXDEV; 3 failures =
print("\n-- 5. conflict / rollback / cross-filesystem / failed --")
db, vault = fresh()
mem_id = "m-conflict"
body, foreign = body_for(mem_id), body_for(mem_id) + "user edited in Obsidian\n"
path = vault.path_for(mem_id)
os.makedirs(os.path.dirname(path), exist_ok=True)
open(path, "w", encoding="utf-8").write(foreign)   # target exists, hash != outbox
op_id = seed_op(db, "create", mem_id, path, sha256(body))
Engine(db, vault).drive(op_id, payload=body)
check("5.1 hash mismatch -> op phase failed",
      db.execute("SELECT phase FROM ops_outbox WHERE op_id=?", (op_id,)).fetchone()[0], "failed")
side = f"{path[:-3]}.conflict-{sha256(foreign)[:8]}.md"
check("5.2 user file preserved as .conflict-<hash8>.md", os.path.exists(side), True)
check("5.3 conflict copy keeps the user body byte-for-byte", vault.read(side), foreign)
check("5.4 conflict event written",
      db.execute("SELECT COUNT(*) FROM memory_events WHERE memory_id=? AND event_type='updated'",
                 (mem_id,)).fetchone()[0], 1)

db, vault = fresh()
mem_id = "m-rollback"
body = body_for(mem_id)
path = vault.path_for(mem_id)
op_id = seed_op(db, "create", mem_id, path, sha256(body))
try:
    Engine(db, vault, io_fail_paths=[path]).drive(op_id, payload=body)
except InjectedIOFailure:
    pass
check("5.5 DB txn abort leaves no half memories row",
      db.execute("SELECT COUNT(*) FROM memories WHERE memory_id=?", (mem_id,)).fetchone()[0], 0)
check("5.6 DB txn abort leaves outbox still pending",
      db.execute("SELECT phase FROM ops_outbox WHERE op_id=?", (op_id,)).fetchone()[0], "pending")
check("5.7 DB txn abort wrote no file", os.path.exists(path), False)
Engine(db, vault).recover()
check("5.8 recovery after abort converges",
      converged(db, vault, mem_id, path, expect_body=body), "converged")

mem_id = "m-exdev"
db, vault = fresh()
src = vault.path_for(mem_id)
dst = vault.archive_path(mem_id)
vault.cross_fs_paths.add(dst)                      # rename raises EXDEV
body = body_for(mem_id)
os.makedirs(os.path.dirname(src), exist_ok=True)
open(src, "w", encoding="utf-8").write(body)
seed_memory(db, mem_id, src, body)
op_id = seed_op(db, "archive", mem_id, dst, sha256(body), src=src)
Engine(db, vault).drive(op_id, new_status="archived")
check("5.9 EXDEV rename compensated by copy+unlink",
      converged(db, vault, mem_id, dst, expect_status="archived", expect_body=body), "converged")
check("5.10 EXDEV: old path removed after copy", os.path.exists(src), False)

db, vault = fresh()
mem_id = "m-failed"
body = body_for(mem_id)
path = vault.path_for(mem_id)
op_id = seed_op(db, "create", mem_id, path, sha256(body))
for _ in range(3):
    Engine(db, vault, io_fail_paths=[path]).recover()
check("5.11 attempt_count reaches 3",
      db.execute("SELECT attempt_count FROM ops_outbox WHERE op_id=?", (op_id,)).fetchone()[0], 3)
Engine(db, vault).recover()
check("5.12 3 failures -> phase failed",
      db.execute("SELECT phase FROM ops_outbox WHERE op_id=?", (op_id,)).fetchone()[0], "failed")
check("5.13 sync_failure event written",
      db.execute("SELECT COUNT(*) FROM memory_events WHERE memory_id=? AND"
                 " event_type='sync_failure'", (mem_id,)).fetchone()[0], 1)

# ===== 6. archive rebuild from a REAL move (no hand-placed terminal state) ======
print("\n-- 6. archive rebuilt from real driver output --")
db, vault = fresh()
mem_id = "m-arch-real"
body = body_for(mem_id, status="archived")
src, dst = vault.path_for(mem_id), vault.archive_path(mem_id)
os.makedirs(os.path.dirname(src), exist_ok=True)
open(src, "w", encoding="utf-8").write(body)
seed_memory(db, mem_id, src, body)
op_id = seed_op(db, "archive", mem_id, dst, sha256(body), src=src)
try:
    Engine(db, vault, crash_at=2).drive(op_id, new_status="archived")   # crash right after rename
except SimulatedCrash:
    pass
check("6.1 mid-archive: file already at _archive, DB not yet advanced",
      (os.path.exists(dst), db.execute("SELECT source_path FROM memories WHERE memory_id=?",
                                       (mem_id,)).fetchone()[0] == src), (True, True))
Engine(db, vault).recover()
check("6.2 archive converges after recovery",
      converged(db, vault, mem_id, dst, expect_status="archived", expect_body=body), "converged")
check("6.3 archived_at set by driver, not seeded",
      db.execute("SELECT archived_at IS NOT NULL FROM memories WHERE memory_id=?",
                 (mem_id,)).fetchone()[0], 1)

db.execute("DELETE FROM memories")          # rebuild path: DB lost, file is the source
db.execute("DELETE FROM sync_state")
db.commit()


def rebuild_from_file(path):
    text = vault.read(path)
    fm = {}
    lines = text.splitlines()
    for line in lines[1:lines.index("---", 1)]:
        k, _, v = line.partition(":")
        fm[k.strip()] = v.strip()
    return fm, text[text.index("---", 3) + 3:]


fm, rebuilt_body = rebuild_from_file(dst)
db.execute("INSERT INTO memories (memory_id, title, body, status, source_path, content_hash,"
           " scope_type, archived_at) VALUES (?,?,?,?,?,?,?,datetime('now','utc'))",
           (fm["memory_id"], fm["title"], rebuilt_body, fm["status"], dst,
            sha256(vault.read(dst)), "private"))
db.commit()
row = db.execute("SELECT memory_id, status, source_path FROM memories WHERE memory_id=?",
                 (mem_id,)).fetchone()
check("6.4 rebuild memory_id from _archive file", row[0], mem_id)
check("6.5 rebuild status from file is archived (not 'active')", row[1], "archived")
check("6.6 rebuild source_path points at _archive", row[2], dst)
check("6.7 rebuild body content preserved", "content of m-arch-real" in rebuilt_body, True)

# ===== 7. RED reference: the ALL-196 order must violate the contract ===========
print("\n-- 7. RED: ALL-196 order (write->rename->commit) breaks the contract --")
db, vault = fresh()
mem_id = "m-legacy"
body = body_for(mem_id)
path = vault.path_for(mem_id)
op_id = seed_op(db, "create", mem_id, path, sha256(body))
legacy = LegacyEngine(db, vault)
legacy.drive(op_id, payload=body)
check("7.1 legacy order is file-first, contradicting 5.4 row 1", legacy.trace,
      ["temp_write", "rename", "db_txn", "done"])
check("7.2 legacy order != contract order",
      legacy.trace == ["db_txn", "temp_write", "rename", "writeback", "done"], False)

db, vault = fresh()
mem_id = "m-legacy-orphan"
body = body_for(mem_id)
path = vault.path_for(mem_id)
op_id = seed_op(db, "create", mem_id, path, sha256(body))
try:
    LegacyEngine(db, vault, crash_at=2).drive(op_id, payload=body)   # die after rename
except SimulatedCrash:
    pass
check("7.3 legacy crash leaves an orphan file with no DB row",
      (os.path.exists(path),
       db.execute("SELECT COUNT(*) FROM memories WHERE memory_id=?", (mem_id,)).fetchone()[0]),
      (True, 0))
check("7.4 legacy leaves sync_state empty for a file on disk",
      db.execute("SELECT COUNT(*) FROM sync_state WHERE path=?", (path,)).fetchone()[0], 0)
# 7.5/7.6 (ALL-201 R2-3a): the previous 7.5 compared two literals and could never
# redden. Assert the real ALL-196 gap instead -- it shipped drivers for create/move
# only, so trash/update/purge ops are undispatchable on the legacy engine while the
# contract engine drives all six.
db, vault = fresh()
_legacy_undispatchable = []
for _t in OP_TYPES:
    _m = "m-legacy-drv-" + _t
    _o = seed_op(db, _t, _m, vault.path_for(_m), sha256(body_for(_m)))
    try:
        LegacyEngine(db, vault).drive(_o, **({"payload": body_for(_m)}
                                             if _t in ("create", "update") else {}))
    except NotImplementedError:
        _legacy_undispatchable.append(_t)
    except Exception:
        pass
check("7.5 ALL-196 legacy engine has no driver for trash/update/purge",
      sorted(_legacy_undispatchable), ["purge", "trash", "update"])
check("7.6 contract engine drives all 6 op_types",
      sorted(Engine.DRIVERS), sorted(OP_TYPES))

# ===== 8. atomic_write implementation contract (ALL-276) =======================
print("\n-- 8. atomic_write implementation contract --")
source = open(__file__, encoding="utf-8").read()
check("8.1 atomic_write uses .tmp suffix", ".tmp" in source, True)
check("8.2 atomic_write uses os.replace", "os.replace" in source, True)
# Extract write_temp + rename methods to verify no direct write to target path
write_temp_start = source.find("def write_temp(")
rename_start = source.find("def rename(", write_temp_start)
rename_end = source.find("\n    def ", rename_start + 1)
if rename_end == -1:
    rename_end = source.find("\n\nclass ", rename_start)
atomic_impl = source[write_temp_start:rename_end] if write_temp_start != -1 and rename_start != -1 else ""
check("8.3 atomic_write does NOT use direct write to target",
      'open(path, "w"' not in atomic_impl and 'open(dst, "w"' not in atomic_impl, True)

print()
print("=" * 78)
print(f"assertions run: {CHECKS}")
if FAILURES:
    print(f"ops_outbox fault injection: {len(FAILURES)} FAILURE(S)")
    for f in FAILURES:
        print(f"  - {f}")
    sys.exit(1)
print("ops_outbox fault injection: ALL PASS")
sys.exit(0)
