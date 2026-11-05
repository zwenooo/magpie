cask "magpie-app" do
  version "0.1.3001-vb.0.0.3"
  sha256 "585212a8cf4de2b2fc2cb158c0146f312c2a252f8987962039dc53c204daf3e5"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.3/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
