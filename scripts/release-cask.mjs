import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [tag, archive, destination] = process.argv.slice(2);
if (!/^v\d+\.\d+\.\d+$/.test(tag ?? "") || !archive || !destination) {
  throw new Error("Usage: release-cask.mjs vX.Y.Z app-archive.zip tap-checkout");
}
if (!existsSync(archive)) throw new Error(`Missing app release archive: ${archive}`);

const version = tag.slice(1);
const cask = join(destination, "Casks/scratchpad.rb");
if (existsSync(cask)) {
  const current = readFileSync(cask, "utf8").match(
    /^  version "(\d+\.\d+\.\d+)"$/m,
  )?.[1];
  if (!current) throw new Error("Refusing to overwrite an unrecognized Scratchpad cask version");
  const before = current.split(".").map(Number);
  const after = version.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if (after[i] < before[i]) throw new Error("Refusing to downgrade the tap");
    if (after[i] > before[i]) break;
  }
}

const sha = createHash("sha256").update(readFileSync(archive)).digest("hex");
const filename = `scratchpad-${tag}-darwin-arm64.zip`;
const body = `cask "scratchpad" do
  version "${version}"
  sha256 "${sha}"

  url "https://github.com/alexcatdad/scratchpad/releases/download/${tag}/${filename}"
  name "Scratchpad"
  desc "Menu bar app for private developer memory"
  homepage "https://github.com/alexcatdad/scratchpad"

  depends_on arch: :arm64
  depends_on macos: :ventura

  app "Scratchpad.app"

  uninstall quit: "com.alexcatdad.scratchpad"
end
`;
mkdirSync(join(destination, "Casks"), { recursive: true });
writeFileSync(cask, body);
console.log(`cask_path=${cask}`);
