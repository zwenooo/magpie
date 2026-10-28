class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.1725-vb.0.0.5"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.5/magpie-cli-darwin-arm64"
      sha256 "ee19166b20ef1c7d842e005245e4b8fb4c26a0d88a42821baf57c5accc2081f7"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.5/magpie-cli-darwin-amd64"
      sha256 "e6a114f50b00a0cc92f02c3667b2a544cc98e66cb78411e175aa9a00fa826c89"
    end
  end
  on_linux do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.5/magpie-cli-linux-arm64"
      sha256 "538c952258581ac8adece5bb0d17e68891affa98d0e856090bf8745c968bd276"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.5/magpie-cli-linux-amd64"
      sha256 "e5d1a0e06766efd7e42f33c6231aeb2830921626506a5ad94bfecbe0c3b0eb39"
    end
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
