cask "magpie-app" do
  arch arm: "arm64", intel: "amd64"

  version "0.1.1725-vb.0.0.5"
  sha256 arm:   "1239621f9e406775fde36f2157b0ffd3b16e321583853ab0c86b1b4558a0d5c9",
         intel: "0b5f19766d4c6c64d90c4931672b846405be5bf881fb792f2eb2926d78e4c353"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.5/magpie-darwin-#{arch}.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  # This release predates in-app updates; upgrade it through Homebrew.
  depends_on :macos

  app "magpie.app"
end
