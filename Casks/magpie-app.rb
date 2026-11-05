cask "magpie-app" do
  arch arm: "arm64", intel: "amd64"

  version "0.1.3001-vb.0.0.1"
  sha256 arm:   "612f736eb0b447bd45586b87bea9c2d73552a86fc331123573c1f60c896924a1",
         intel: "a4e9e60b4917cb1463d337f8d02f821a0c046a7eb217d4b518fd87cd6211f798"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.1/magpie-darwin-#{arch}.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on :macos

  app "magpie.app"
end
