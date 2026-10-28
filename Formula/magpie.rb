class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.1725-vb.0.0.6"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.6/magpie-cli-darwin-arm64"
      sha256 "4de4eabab0869ea09c0b4655410b0ccd59c03c5dff0b153d4f85260136af03f8"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.6/magpie-cli-darwin-amd64"
      sha256 "a3d624812dcc230ba600eb63e5593887b19df9a3a50bdc03586b021a99374405"
    end
  end
  on_linux do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.6/magpie-cli-linux-arm64"
      sha256 "7377ad1bfd7b07db2ff3b6b67a43db22adbcd464fc917ac1568559f9dd3a0e1e"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.6/magpie-cli-linux-amd64"
      sha256 "3f00c1a0ea9e8fd631f5b189952c8772b4d67b1a555afd42e0a7c79dd818b27d"
    end
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
