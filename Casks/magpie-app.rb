cask "magpie-app" do
  version "0.1.3001-vb.0.0.2"
  sha256 "6b399374619ed61cdf28c365f1a3325151949f9196357c24038546fcd239394a"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.2/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
