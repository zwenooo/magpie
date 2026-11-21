class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.4562-vb.0.0.2"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4562-vb.0.0.2/magpie-cli-darwin-arm64"
    sha256 "fe0e179538fe08a031f425f88091fae864e82d8e8b38f865838c45adaaedf9a0"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4562-vb.0.0.2/magpie-cli-linux-amd64"
    sha256 "fdd15e9506ea66e7c75c557ae6e691ab77532c8442da148571dc4c2e4e4cfd15"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
