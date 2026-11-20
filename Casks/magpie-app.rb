cask "magpie-app" do
  version "0.1.4562-vb.0.0.1"
  sha256 "a687f5f31b0350e8cdf249fb7137049824c94ce07458a3738a10b033dd9c3c36"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4562-vb.0.0.1/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
