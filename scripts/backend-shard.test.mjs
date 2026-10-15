import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const runner = join(import.meta.dirname, "backend-shard.sh");
const slow = "TestNativeLargeExportUsesBoundedServerMemoryAndRemainsReplayable";

test("backend shards partition real Go tests, subtests, examples and fuzz seeds", () => {
  const cwd = mkdtempSync(join(tmpdir(), "scratchpad-shard-test-"));
  const env = { ...process.env, GORACE: "atexit_sleep_ms=0" };
  const run = (command, args, extraEnv = {}) =>
    spawnSync(command, args, {
      cwd,
      env: { ...env, ...extraEnv },
      encoding: "utf8",
      timeout: 120_000,
    });
  const outcomes = (result) => {
    assert.equal(result.status, 0, result.stdout + result.stderr);
    return result.stdout
      .split("\n")
      .filter(Boolean)
      .map((line) => JSON.parse(line))
      .filter(
        (row) => row.Test && ["pass", "skip", "fail"].includes(row.Action),
      )
      .map((row) => `${row.Package}/${row.Test}:${row.Action}`)
      .sort();
  };
  try {
    writeFileSync(join(cwd, "go.mod"), "module shardfixture\n\ngo 1.27\n");
    const fixture = `package shardfixture
import ("fmt"; "os"; "testing")
func ${slow}(t *testing.T) {
  t.Run("nested", func(t *testing.T) { if os.Getenv("FAIL_SHARD") == "1" { t.Fatal("synthetic failure") } })
}
func ${slow}Suffix(t *testing.T) {}
func TestNewlyAdded(t *testing.T) {
  t.Run("${slow}", func(t *testing.T) { if os.Getenv("FAIL_SHARD") == "2" { t.Fatal("synthetic failure") } })
}
func TestExpectedSkip(t *testing.T) { t.Skip("synthetic optional test") }
func Example() { fmt.Println("example"); /* Output: example */ }
func FuzzSeed(f *testing.F) { f.Add("seed"); f.Fuzz(func(t *testing.T, value string) {}) }
`;
    writeFileSync(join(cwd, "partition_test.go"), fixture);
    mkdirSync(join(cwd, "another"));
    writeFileSync(join(cwd, "another", "partition_test.go"), fixture);
    const full = outcomes(
      run("go", ["test", "-json", "-race", "-count=1", "./..."]),
    );
    const first = outcomes(run("bash", [runner, "1"]));
    const second = outcomes(run("bash", [runner, "2"]));
    assert.ok(first.length > 0 && second.length > 0);
    assert.equal(first.filter((item) => second.includes(item)).length, 0);
    assert.deepEqual([...first, ...second].sort(), full);
    assert.ok(second.some((item) => item.includes("Suffix:pass")));
    assert.ok(second.some((item) => item.includes("Example:pass")));
    assert.ok(second.some((item) => item.includes("FuzzSeed/seed#0:pass")));
    for (const shard of ["1", "2"]) {
      const failed = run("bash", [runner, shard], { FAIL_SHARD: shard });
      assert.equal(failed.status, 1, failed.stdout + failed.stderr);
      assert.match(failed.stdout, /synthetic failure/);
    }
    for (const shard of ["", "0", "3", "all"]) {
      assert.equal(run("bash", [runner, shard]).status, 2);
    }
  } finally {
    rmSync(cwd, { recursive: true, force: true });
  }
});
