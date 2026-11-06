cask "magpie-app" do
  version "0.1.3001-vb.0.0.4"
  sha256 "5a5f6bf70d9e28a27e9dee111d938b8cfd8df55469e46da47a7545fc8f9464ff"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.4/magpie-darwin-arm64.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on arch: :arm64
  depends_on :macos

  app "magpie.app"
end
