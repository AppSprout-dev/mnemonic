"""Tests for session.py utility functions."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from agent.session import MAX_SESSIONS, _record_task


def _record(tmpdir, session_id="sess-abc", description="do something", **kwargs):
    defaults = dict(
        evolution_dir=tmpdir,
        session_id=session_id,
        model="claude-sonnet-4-6",
        description=description,
        started="2026-02-25T00:00:00+00:00",
        duration_ms=1000,
        cost_usd=0.001,
        turns=3,
        evolved=False,
    )
    defaults.update(kwargs)
    _record_task(**defaults)


def _load(tmpdir):
    return json.loads((Path(tmpdir) / "sessions.json").read_text())


class TestRecordTask(unittest.TestCase):
    def test_creates_file_on_first_call(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            _record(tmpdir)
            data = _load(tmpdir)
            self.assertEqual(len(data["sessions"]), 1)
            self.assertEqual(data["sessions"][0]["id"], "sess-abc")
            self.assertEqual(len(data["sessions"][0]["tasks"]), 1)
            self.assertEqual(data["sessions"][0]["tasks"][0]["description"], "do something")

    def test_appends_task_to_existing_session(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            _record(tmpdir, session_id="s1", description="task 1")
            _record(tmpdir, session_id="s1", description="task 2")
            data = _load(tmpdir)
            self.assertEqual(len(data["sessions"]), 1)
            self.assertEqual(len(data["sessions"][0]["tasks"]), 2)
            self.assertEqual(data["sessions"][0]["tasks"][1]["description"], "task 2")

    def test_creates_new_session_for_different_id(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            _record(tmpdir, session_id="s1")
            _record(tmpdir, session_id="s2")
            data = _load(tmpdir)
            self.assertEqual(len(data["sessions"]), 2)
            ids = [s["id"] for s in data["sessions"]]
            self.assertIn("s1", ids)
            self.assertIn("s2", ids)

    def test_recovers_from_malformed_json(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            (Path(tmpdir) / "sessions.json").write_text("not valid json{{{")
            _record(tmpdir)
            data = _load(tmpdir)
            self.assertEqual(len(data["sessions"]), 1)

    def test_truncates_long_description(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            _record(tmpdir, description="x" * 300)
            data = _load(tmpdir)
            saved = data["sessions"][0]["tasks"][0]["description"]
            self.assertEqual(len(saved), 200)

    def test_records_cost_rounded_and_turns(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            _record(tmpdir, cost_usd=0.0123456789, turns=7)
            data = _load(tmpdir)
            task = data["sessions"][0]["tasks"][0]
            self.assertEqual(task["turns"], 7)
            self.assertEqual(task["cost_usd"], round(0.0123456789, 6))

    def test_records_evolved_flag(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            _record(tmpdir, evolved=True)
            data = _load(tmpdir)
            self.assertTrue(data["sessions"][0]["tasks"][0]["evolved"])

    def test_rotates_old_sessions(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            for i in range(MAX_SESSIONS + 3):
                _record(tmpdir, session_id=f"s{i}")
            data = _load(tmpdir)
            self.assertLessEqual(len(data["sessions"]), MAX_SESSIONS)

    def test_rotation_preserves_most_recent(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            for i in range(MAX_SESSIONS + 3):
                _record(tmpdir, session_id=f"s{i}")
            data = _load(tmpdir)
            ids = [s["id"] for s in data["sessions"]]
            # Most recent sessions should be present
            self.assertIn(f"s{MAX_SESSIONS + 2}", ids)
            self.assertIn(f"s{MAX_SESSIONS + 1}", ids)
            # Oldest should be gone
            self.assertNotIn("s0", ids)

    def test_oserror_on_write_does_not_raise(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            sessions_path = Path(tmpdir) / "sessions.json"
            sessions_path.write_text('{"sessions": []}')
            sessions_path.chmod(0o444)
            try:
                _record(tmpdir)  # Should not raise
            except OSError:
                self.fail("_record_task should not raise OSError on write failure")
            finally:
                sessions_path.chmod(0o644)

    def test_missing_evolution_dir_does_not_raise(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            nonexistent = str(Path(tmpdir) / "no_such_dir")
            # Should not raise even if dir doesn't exist (file write will fail silently)
            try:
                _record(nonexistent)
            except Exception as e:
                self.fail(f"_record_task raised unexpectedly: {e}")


if __name__ == "__main__":
    unittest.main()
