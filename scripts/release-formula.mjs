import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [tag, assets, destination] = process.argv.slice(2);
if (!/^v\d+\.\d+\.\d+$/.test(tag ?? "") || !assets || !destination) {
  throw new Error(
    "Usage: release-formula.mjs vX.Y.Z verified-assets tap-checkout",
  );
}
const version = tag.slice(1);
const formula = join(destination, "Formula/scratchpad-mcp.rb");
if (existsSync(formula)) {
  const current = readFileSync(formula, "utf8").match(
    /^ {2}version "(\d+\.\d+\.\d+)"$/m,
  )?.[1];
  if (!current)
    throw new Error("Refusing to overwrite an unrecognized formula version");
  const before = current.split(".").map(Number);
  const after = version.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if (after[i] < before[i]) throw new Error("Refusing to downgrade the tap");
    if (after[i] > before[i]) break;
  }
}
function source(os, arch) {
  const filename = `scratchpad-mcp-${tag}-${os}-${arch}.${os === "darwin" ? "zip" : "tar.gz"}`;
  const sha = createHash("sha256")
    .update(readFileSync(join(assets, filename)))
    .digest("hex");
  return `        url "https://github.com/alexcatdad/scratchpad/releases/download/${tag}/${filename}"\n        sha256 "${sha}"`;
}
const body = `class ScratchpadMcp < Formula
  desc "Private project memory for developers and coding agents"
  homepage "https://github.com/alexcatdad/scratchpad"
  version "${version}"

  depends_on "git"
  depends_on "openssh"

  on_macos do
    on_arm do
${source("darwin", "arm64")}
    end
    on_intel do
${source("darwin", "amd64")}
    end
  end

  on_linux do
    on_arm do
${source("linux", "arm64")}
    end
    on_intel do
${source("linux", "amd64")}
    end
  end

  def install
    bin.install "scratchpad-mcp"
  end

  test do
    assert_equal "scratchpad-mcp #{version}", shell_output("#{bin}/scratchpad-mcp --version").strip
  end
end
`;
mkdirSync(join(destination, "Formula"), { recursive: true });
writeFileSync(formula, body);
