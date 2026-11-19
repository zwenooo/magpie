cask "magpie-app" do
  version "0.1.4501-vb.0.0.1"
  sha256 "b33f153c3f6d7d0938bdab372c274704d0b9e10074c8c6075c50583590702adb"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4501-vb.0.0.1/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
