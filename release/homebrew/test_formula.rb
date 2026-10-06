# This test evaluates only our formula methods against a tiny DSL stub.
# It never invokes Homebrew, downloads an archive or launches an agent.
require "minitest/autorun"
require "pathname"

class Formula
  class << self
    attr_accessor :requirements
    def desc(*) end
    def homepage(*) end
    def url(*) end
    def version(*) end
    def sha256(*) end
    def license(*) end
    def depends_on(value)
      self.requirements ||= []
      self.requirements << value
    end
    def test(&block) end
  end
  attr_reader :commands, :installed
  attr_accessor :install_failure, :restart_failure
  def initialize
    @commands = []
    @installed = []
  end
  def system(*args)
    @commands << args.map(&:to_s)
    raise "restart failed" if restart_failure
    true
  end
end

# bin.install must dispatch to an installer, not recursively to the formula.
class FakeBin
  def initialize(formula); @formula = formula; end
  def /(name); Pathname.new("/isolated/Cellar/cercano/2/bin")/name; end
  def install(*files)
    raise "install failed" if @formula.install_failure
    @formula.installed.concat(files)
  end
end

load File.join(__dir__, "cercano.rb.in")

class FormulaHookTest < Minitest::Test
  def setup
    @formula = Cercano.new
    @formula.define_singleton_method(:bin) { FakeBin.new(self) }
  end

  def test_install_only_places_both_binaries
    @formula.install
    assert_equal ["bin/cercano", "bin/cercano-cli"], @formula.installed
    assert_empty @formula.commands
  end

  def test_failed_install_does_not_request_restart
    @formula.install_failure = true
    assert_raises(RuntimeError) { @formula.install }
    assert_empty @formula.commands
  end

  def test_post_install_uses_new_keg_not_path
    @formula.post_install
    assert_equal [["/isolated/Cellar/cercano/2/bin/cercano", "restart-after-upgrade"]], @formula.commands
  end

  def test_restart_failure_is_not_silenced
    @formula.restart_failure = true
    assert_raises(RuntimeError) { @formula.post_install }
  end

  def test_platform_constraints
    # Monterey is macOS 12, matching the binaries' deployment target, so
    # Homebrew refuses older systems instead of installing binaries that
    # cannot run. Intel Macs are excluded for this release.
    assert_includes Cercano.requirements, {macos: :monterey}
    assert_includes Cercano.requirements, {arch: :arm64}
  end

  def test_smoke_test_does_not_restart_a_running_agent
    # `brew test` must never act on the user's live agent. Verified against
    # real builds: restart-after-upgrade without --help attempts a restart.
    source = File.read(File.join(__dir__, "cercano.rb.in"))
    smoke = source[/^  test do$.*?^  end$/m]
    refute_nil smoke, "formula must define a test block"
    smoke.scan(/restart-after-upgrade[^"]*/) do |invocation|
      assert_includes invocation, "--help",
                      "brew test may only invoke restart-after-upgrade with --help"
    end
  end
end
