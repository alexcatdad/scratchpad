import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const rubyCaskHarness = `
require "fileutils"
require "json"
require "pathname"

class ScratchpadTestCask
  attr_reader :commands

  def initialize
    @commands = []
    @hooks = {}
  end

  def cask(_name, &block)
    instance_eval(&block)
  end
  def version(_value); end
  def sha256(_value); end
  def url(_value); end
  def name(_value); end
  def desc(_value); end
  def homepage(_value); end
  def depends_on(**_values); end
  def app(_value); end
  def uninstall(**_values); end
  def preflight(&block)
    @hooks[:preflight] = block
  end

  def uninstall_preflight(&block)
    @hooks[:uninstall_preflight] = block
  end

  def staged_path
    Pathname.new(ENV.fetch("SCRATCHPAD_STAGED_PATH"))
  end

  def appdir
    Pathname.new(ENV.fetch("SCRATCHPAD_APP_DIR"))
  end

  def system_command(path, args:, must_succeed: false)
    @commands << { path: path, args: args, must_succeed: must_succeed }
    failed = ENV["SCRATCHPAD_FAIL_PREPARE"] == "1" && args.include?("--prepare-upgrade")
    raise "synthetic prepare failure" if failed && must_succeed
    !failed
  end

  def run_hook(name)
    @hooks.fetch(name).call
  end
end

$scratchpad_cask = ScratchpadTestCask.new
def cask(name, &block)
  $scratchpad_cask.cask(name, &block)
end
eval(File.read(ARGV.fetch(0)), TOPLEVEL_BINDING, "generated-cask.rb")

failure = nil
begin
  $scratchpad_cask.run_hook(:preflight)
  if ENV["SCRATCHPAD_RUN_UNINSTALL"] == "1"
    $scratchpad_cask.run_hook(:uninstall_preflight)
  end
rescue => error
  failure = error.message
end
lock_path = File.join(ENV.fetch("HOME"), "Library/Application Support/Scratchpad/homebrew-upgrade.lock")
probe = Process.spawn("ruby", "-e", "file = File.open(ARGV.fetch(0), File::RDWR); exit(file.flock(File::LOCK_EX | File::LOCK_NB) ? 0 : 7)", lock_path, out: File::NULL, err: File::NULL)
Process.wait(probe)
puts JSON.generate({ failure: failure, commands: $scratchpad_cask.commands, lock_busy: $?.exitstatus == 7 })
`;

const rubyTryLock = `
file = File.open(ARGV.fetch(0), File::RDWR)
exit(file.flock(File::LOCK_EX | File::LOCK_NB) ? 0 : 7)
`;

function runRubyCaskHarness(
  caskPath,
  home,
  stagedPath,
  appDir,
  { failPrepare = false, runUninstall = false } = {},
) {
  return spawnSync("ruby", ["-e", rubyCaskHarness, caskPath], {
    env: {
      ...process.env,
      HOME: home,
      SCRATCHPAD_STAGED_PATH: stagedPath,
      SCRATCHPAD_APP_DIR: appDir,
      SCRATCHPAD_FAIL_PREPARE: failPrepare ? "1" : "0",
      SCRATCHPAD_RUN_UNINSTALL: runUninstall ? "1" : "0",
    },
    encoding: "utf8",
    timeout: 10000,
  });
}

function assertRubyLockFree(lockPath) {
  const result = spawnSync("ruby", ["-e", rubyTryLock, lockPath], {
    encoding: "utf8",
  });
  assert.equal(
    result.status,
    0,
    result.stderr || `expected free lock, got ${result.status}`,
  );
}

test("release-cask renders the pinned arm64 app archive, runs safe hooks, and refuses downgrades", () => {
  const root = mkdtempSync(join(tmpdir(), "scratchpad-release-cask-"));
  try {
    const archive = join(root, "scratchpad-v1.2.3-darwin-arm64.zip");
    const tap = join(root, "tap");
    mkdirSync(tap);
    const bytes = Buffer.from("synthetic signed app archive");
    writeFileSync(archive, bytes);
    const result = (tag) =>
      spawnSync(
        process.execPath,
        ["scripts/release-cask.mjs", tag, archive, tap],
        { cwd: process.cwd(), encoding: "utf8" },
      );

    const first = result("v1.2.3");
    assert.equal(first.status, 0, first.stderr);
    const cask = readFileSync(join(tap, "Casks/scratchpad.rb"), "utf8");
    assert.match(cask, /cask "scratchpad" do/);
    assert.match(cask, /version "1\.2\.3"/);
    assert.match(
      cask,
      new RegExp(
        `sha256 "${createHash("sha256").update(bytes).digest("hex")}"`,
      ),
    );
    assert.match(cask, /scratchpad-v1\.2\.3-darwin-arm64\.zip/);
    assert.match(cask, /depends_on arch: :arm64/);
    assert.match(cask, /depends_on macos: :ventura/);
    assert.match(cask, /app "Scratchpad\.app"/);
    assert.match(cask, /uninstall quit: "com\.alexcatdad\.scratchpad"/);
    assert.match(cask, /preflight do/);
    assert.match(cask, /uninstall_preflight do/);
    assert.match(cask, /homebrew-upgrade\.lock/);
    assert.match(cask, /args: \["--prepare-upgrade"/);
    assert.match(cask, /must_succeed: true/);
    assert.doesNotMatch(cask, /postflight|finish-upgrade/);
    assert.doesNotMatch(cask, /verified:|no_quarantine|quarantine/);

    const rubyVersion = spawnSync("ruby", ["--version"], { encoding: "utf8" });
    assert.equal(rubyVersion.status, 0, rubyVersion.stderr);
    const home = join(root, "synthetic-home");
    const stagedPath = join(root, "staged path");
    const appDir = join(root, "custom app directory");
    const caskPath = join(tap, "Casks/scratchpad.rb");
    mkdirSync(home);
    mkdirSync(stagedPath);
    mkdirSync(appDir);
    const lockPath = join(
      home,
      "Library/Application Support/Scratchpad/homebrew-upgrade.lock",
    );
    const stagedService = join(
      stagedPath,
      "Scratchpad.app/Contents/Helpers/scratchpad-service",
    );
    const installedService = join(
      appDir,
      "Scratchpad.app/Contents/Helpers/scratchpad-service",
    );

    const successHarness = runRubyCaskHarness(
      caskPath,
      home,
      stagedPath,
      appDir,
      { runUninstall: true },
    );
    assert.equal(successHarness.status, 0, successHarness.stderr);
    const success = JSON.parse(successHarness.stdout);
    assert.equal(success.failure, null);
    assert.deepEqual(success.commands, [
      {
        path: stagedService,
        args: ["--prepare-upgrade", "--upgrade-service-path", installedService],
        must_succeed: true,
      },
      {
        path: installedService,
        args: ["--prepare-upgrade"],
        must_succeed: true,
      },
    ]);
    assert.equal(
      success.lock_busy,
      true,
      "the lock must remain held across both hook invocations",
    );
    assertRubyLockFree(lockPath);

    const failedHarness = runRubyCaskHarness(
      caskPath,
      home,
      stagedPath,
      appDir,
      { failPrepare: true },
    );
    assert.equal(failedHarness.status, 0, failedHarness.stderr);
    const failure = JSON.parse(failedHarness.stdout);
    assert.match(failure.failure, /synthetic prepare failure/);
    assert.equal(failure.commands.length, 1);
    assert.equal(failure.commands[0].path, stagedService);
    assert.equal(failure.commands[0].must_succeed, true);
    assert.equal(
      failure.lock_busy,
      true,
      "a failed prepare must retain the lock until Ruby exits",
    );
    assertRubyLockFree(lockPath);

    const downgrade = result("v1.2.2");
    assert.notEqual(downgrade.status, 0);
    assert.match(downgrade.stderr, /Refusing to downgrade/);
    assert.equal(readFileSync(join(tap, "Casks/scratchpad.rb"), "utf8"), cask);
    assert.notEqual(result("invalid").status, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
