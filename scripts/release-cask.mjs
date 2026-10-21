import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [tag, archive, destination] = process.argv.slice(2);
const usage = "Usage: release-cask.mjs vX.Y.Z app-archive.zip tap-checkout";
const unrecognizedVersion = "Unrecognized cask version";
if (!/^v\d+\.\d+\.\d+$/.test(tag ?? "") || !archive || !destination) {
  throw new Error(usage);
}
if (!existsSync(archive)) {
  throw new Error(`Missing app release archive: ${archive}`);
}

const version = tag.slice(1);
const cask = join(destination, "Casks/scratchpad.rb");
if (existsSync(cask)) {
  const current = readFileSync(cask, "utf8").match(
    /^ {2}version "(\d+\.\d+\.\d+)"$/m,
  )?.[1];
  if (!current) {
    throw new Error(unrecognizedVersion);
  }
  const before = current.split(".").map(Number);
  const after = version.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if (after[i] < before[i]) throw new Error("Refusing to downgrade the tap");
    if (after[i] > before[i]) break;
  }
}

const sha = createHash("sha256").update(readFileSync(archive)).digest("hex");
const filename = `scratchpad-${tag}-darwin-arm64.zip`;
const body = `# Third-party Ruby hooks retain the transaction lock until Homebrew exits.
# Declarative steps cannot express this lock lifetime.
cask "scratchpad" do
  version "${version}"
  sha256 "${sha}"

  url "https://github.com/alexcatdad/scratchpad/releases/download/${tag}/${filename}"
  name "Scratchpad"
  desc "Menu bar app for private developer memory"
  homepage "https://github.com/alexcatdad/scratchpad"

  depends_on arch: :arm64
  depends_on macos: :ventura

  upgrade_lock = lambda do
    unless Object.const_defined?(:ScratchpadHomebrewUpgradeLock, false)
      lock_dir = File.expand_path("~/Library/Application Support/Scratchpad")
      FileUtils.mkdir_p(lock_dir, mode: 0700)
      dir_stat = File.lstat(lock_dir)
      if !dir_stat.directory? || dir_stat.uid != Process.uid || !dir_stat.mode.nobits?(0077)
        raise "Scratchpad upgrade state directory is not private"
      end

      lock_path = File.join(lock_dir, "homebrew-upgrade.lock")
      # This descriptor intentionally remains open for the Homebrew transaction.
      lock = File.open(lock_path, File::RDWR | File::CREAT | File::NOFOLLOW, 0600)
      lock_stat = lock.stat
      if !lock_stat.file? || lock_stat.uid != Process.uid || !lock_stat.mode.nobits?(0077)
        raise "Scratchpad upgrade lock is not private"
      end

      lock.close_on_exec = true
      lock.flock(File::LOCK_EX)
      Object.const_set(:ScratchpadHomebrewUpgradeLock, lock)
    end
  end

  app "Scratchpad.app"

  preflight do
    upgrade_lock.call
    service = staged_path/"Scratchpad.app/Contents/Helpers/scratchpad-service"
    installed_service = appdir/"Scratchpad.app/Contents/Helpers/scratchpad-service"
    system_command service.to_s,
                   args:         ["--prepare-upgrade", "--upgrade-service-path", installed_service.to_s],
                   must_succeed: true
  end

  uninstall_preflight do
    upgrade_lock.call
    service = appdir/"Scratchpad.app/Contents/Helpers/scratchpad-service"
    system_command service.to_s,
                   args:         ["--prepare-upgrade"],
                   must_succeed: true
  end

  uninstall quit: "com.alexcatdad.scratchpad"
end
`;
mkdirSync(join(destination, "Casks"), { recursive: true });
writeFileSync(cask, body);
console.log(`cask_path=${cask}`);
