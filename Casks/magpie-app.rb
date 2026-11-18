cask "magpie-app" do
  version "0.1.3140-vb.0.0.3"
  sha256 "a3ce9bcf67c7560168683627cdd571f2686daa88b25a76243535192176f96d51"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3140-vb.0.0.3/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
