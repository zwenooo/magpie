cask "magpie-app" do
  version "0.1.4562-vb.0.0.2"
  sha256 "3809c567f91bd73ad5d87ee08a833354c606489387f611312b6daebba44359f5"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4562-vb.0.0.2/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
