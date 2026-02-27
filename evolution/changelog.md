# Evolution Changelog

## 2026-02-25

### Initialized evolution directory
- Created principles.yaml, strategies.yaml, prompt_patches.yaml, changelog.md
- Migrated known principles (p1–p9) from system prompt into principles.yaml

### Added p10: DB-first system audit
- Source: Full Mnemonic system audit revealed that project-local `mnemonic.db` files were empty stubs; real data at `~/.mnemonic/memory.db`
- Querying the runtime DB confirmed all major hypotheses faster than code reading alone
- Confidence: 0.8

### Added p11: Verify subagent line-number claims
- Source: Explore subagent gave detailed file:line bug reports — concepts were correct but specific line numbers needed verification with Read tool
- Prevents reporting fabricated specifics as confirmed findings
- Confidence: 0.7

### Added prompt_audit strategy
- New task type covering systematic LLM prompt audits across a codebase
- Key steps: provider-first reading, grep for all call sites, system-role check, targeted reads per site
- Tips: large files need grep-first approach; inline persona is a system-prompt substitute; extractJSON is a smell
- Rationale: grepping for llmProvider.Complete found all 10 call sites in one shot vs. sequential file reads

### Added p12: symbol-targeted grep before reading
- Confirmed via prompt audit: grepping for a symbol first gives complete inventory, then targeted reads
- More efficient than sequential file reads, prevents missing call sites in large files
- Confidence: 0.7

### Updated codebase_audit strategy
- Added step: "Locate the actual RUNNING database — may differ from project-local DB files"
- Added step: "Query the runtime DB directly before code analysis"
- Added step: "Verify subagent file:line claims with targeted Read calls"
- Added tips about meta_observations table being the highest-signal starting point
- Rationale: Previous strategy missed that the DB is the ground truth source; code analysis alone leaves bugs unconfirmed
