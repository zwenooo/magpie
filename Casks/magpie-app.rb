cask "magpie-app" do
  arch arm: "arm64", intel: "amd64"

  version "0.1.2909-vb.0.0.5"
  sha256 arm:   "d49a5bb9032955a041aaaf9af320a291521145c6f87af26fc589868042470dda",
         intel: "2204ef7d7f9dcca6a2bb99ebb4d0edb0fa1f739f7f4626ef3e6da59f1a39a999"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2909-vb.0.0.5/magpie-darwin-#{arch}.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on :macos

  app "magpie.app"
end
