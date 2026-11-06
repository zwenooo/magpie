cask "magpie-app" do
  version "0.1.3140-vb.0.0.2"
  sha256 "69de9c9333e63f5fa3a3c96bfb37fc908b3dbb2613089f341fe527f6215941d6"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3140-vb.0.0.2/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
