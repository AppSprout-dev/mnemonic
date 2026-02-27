"""Tests for the PostToolUse hook."""

import asyncio
import unittest

from agent.hooks import post_tool_use_hook


class TestPostToolUseHook(unittest.TestCase):
    def _run(self, coro):
        return asyncio.run(coro)

    def test_write_nudges_memory(self):
        result = self._run(
            post_tool_use_hook(
                {"tool_name": "Write", "tool_input": {"file_path": "/tmp/foo.py"}, "tool_response": {}},
                "tu_123",
                None,
            )
        )
        self.assertIn("systemMessage", result)
        self.assertIn("/tmp/foo.py", result["systemMessage"])
        self.assertIn("mcp__mnemonic__remember", result["systemMessage"])

    def test_edit_nudges_memory(self):
        result = self._run(
            post_tool_use_hook(
                {"tool_name": "Edit", "tool_input": {"file_path": "/tmp/bar.go"}, "tool_response": {}},
                "tu_456",
                None,
            )
        )
        self.assertIn("systemMessage", result)
        self.assertIn("/tmp/bar.go", result["systemMessage"])

    def test_bash_failure_nudges_error_capture(self):
        result = self._run(
            post_tool_use_hook(
                {
                    "tool_name": "Bash",
                    "tool_input": {"command": "go test ./..."},
                    "tool_response": {"exitCode": 1, "output": "FAIL: TestFoo"},
                },
                "tu_789",
                None,
            )
        )
        self.assertIn("systemMessage", result)
        self.assertIn("exit code 1", result["systemMessage"])
        self.assertIn("type='error'", result["systemMessage"])

    def test_bash_success_no_nudge(self):
        result = self._run(
            post_tool_use_hook(
                {
                    "tool_name": "Bash",
                    "tool_input": {"command": "echo ok"},
                    "tool_response": {"exitCode": 0, "output": "ok"},
                },
                "tu_000",
                None,
            )
        )
        self.assertEqual(result, {})

    def test_recall_nudges_feedback(self):
        result = self._run(
            post_tool_use_hook(
                {"tool_name": "mcp__mnemonic__recall", "tool_input": {}, "tool_response": {}},
                "tu_111",
                None,
            )
        )
        self.assertIn("systemMessage", result)
        self.assertIn("feedback", result["systemMessage"])

    def test_unrelated_tool_no_nudge(self):
        result = self._run(
            post_tool_use_hook(
                {"tool_name": "Read", "tool_input": {"file_path": "/tmp/x"}, "tool_response": {}},
                "tu_222",
                None,
            )
        )
        self.assertEqual(result, {})

    def test_bash_failure_truncates_long_output(self):
        long_output = "x" * 1000
        result = self._run(
            post_tool_use_hook(
                {
                    "tool_name": "Bash",
                    "tool_input": {"command": "make build"},
                    "tool_response": {"exitCode": 2, "output": long_output},
                },
                "tu_333",
                None,
            )
        )
        # Only last 300 chars of output should be in the message
        self.assertIn("x" * 300, result["systemMessage"])
        self.assertNotIn("x" * 301, result["systemMessage"])


class TestPromptsAssembly(unittest.TestCase):
    def test_assemble_with_malformed_yaml_does_not_raise(self):
        import tempfile
        from pathlib import Path

        from agent.prompts import assemble_system_prompt

        with tempfile.TemporaryDirectory() as tmpdir:
            # Write invalid YAML to all three evolution files
            (Path(tmpdir) / "principles.yaml").write_text("{ invalid: yaml: :")
            (Path(tmpdir) / "strategies.yaml").write_text("{ invalid: yaml: :")
            (Path(tmpdir) / "prompt_patches.yaml").write_text("{ invalid: yaml: :")

            # Should not raise — returns base prompt without evolution sections
            prompt = assemble_system_prompt(tmpdir)
            self.assertIn("Self-Evolution Protocol", prompt)
            self.assertNotIn("Learned Principles", prompt)
            self.assertNotIn("Task Strategies", prompt)

    def test_assemble_with_empty_evolution(self):
        import tempfile
        from pathlib import Path

        from agent.prompts import assemble_system_prompt

        with tempfile.TemporaryDirectory() as tmpdir:
            # Write empty evolution files
            (Path(tmpdir) / "principles.yaml").write_text("principles: []\n")
            (Path(tmpdir) / "strategies.yaml").write_text("strategies: {}\n")
            (Path(tmpdir) / "prompt_patches.yaml").write_text("patches: []\n")

            prompt = assemble_system_prompt(tmpdir)
            self.assertIn("Self-Evolution Protocol", prompt)
            # No Learned Principles section when empty
            self.assertNotIn("Learned Principles", prompt)

    def test_assemble_with_principles(self):
        import tempfile
        from pathlib import Path

        from agent.prompts import assemble_system_prompt

        with tempfile.TemporaryDirectory() as tmpdir:
            (Path(tmpdir) / "principles.yaml").write_text(
                'principles:\n  - id: p1\n    text: "Always test first"\n'
                '    source: "TDD session"\n    confidence: 0.8\n'
            )
            (Path(tmpdir) / "strategies.yaml").write_text("strategies: {}\n")
            (Path(tmpdir) / "prompt_patches.yaml").write_text("patches: []\n")

            prompt = assemble_system_prompt(tmpdir)
            self.assertIn("Learned Principles", prompt)
            self.assertIn("Always test first", prompt)
            self.assertIn("[0.8]", prompt)

    def test_assemble_with_strategies(self):
        import tempfile
        from pathlib import Path

        from agent.prompts import assemble_system_prompt

        with tempfile.TemporaryDirectory() as tmpdir:
            (Path(tmpdir) / "principles.yaml").write_text("principles: []\n")
            (Path(tmpdir) / "strategies.yaml").write_text(
                "strategies:\n  bug_fix:\n    steps:\n"
                '      - "Reproduce first"\n      - "Write failing test"\n'
            )
            (Path(tmpdir) / "prompt_patches.yaml").write_text("patches: []\n")

            prompt = assemble_system_prompt(tmpdir)
            self.assertIn("Task Strategies", prompt)
            self.assertIn("bug_fix", prompt)
            self.assertIn("Reproduce first", prompt)


if __name__ == "__main__":
    unittest.main()
