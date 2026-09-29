# Scratchpad contributor instructions

Read `docs/prd.md`, `docs/architecture.md`, and `docs/api.md`, including accepted clarifications, before changing behavior. `docs/archive/` is historical, not current authority.

- English only for product UI and documentation.
- Keep useful captures immutable, provenance explicit, central API authoritative, and repository mirroring optional.
- Use maintained dependencies; verify latest stable versions before adding or upgrading them and pin exact versions. Node uses the latest Node 24 LTS patch per the architecture.
- Run the relevant lint, type, test and build checks. GitHub Actions is the canonical runner.
- Add important decisions to `decisions.jsonl`; preserve prior entries. Multi-command project activities need a runbook under `docs/runbooks/`.
- Agent parallelism is allowed. Coordinate file ownership and do not overwrite concurrent work. Finish/stop agents when their task is done.
- Make small coherent commits. Never commit secrets, local databases or test artifacts.
- Development starts with the authenticated SQLite loop; the full MVP remains defined by the PRD. Do not confuse local tests with deployed acceptance.
