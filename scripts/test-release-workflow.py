#!/usr/bin/env python3
"""Structural checks for .github/workflows/release-macos.yml.

A mistake in the release workflow surfaces only during a real signed run, when
credentials are already in play. These tests assert the safety properties that
matter — no untrusted trigger, least privilege, protected environment, keychain
cleanup on every path, verification before publication, and no silent
replacement of published artifacts.

This validates structure only. It does not run the workflow, contact GitHub or
Apple, or prove that a real signed release succeeds.
"""

import json
import tempfile
import subprocess
import sys
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
WORKFLOW_PATH = REPO / ".github" / "workflows" / "release-macos.yml"

try:
    import yaml
except ImportError:  # pragma: no cover - depends on the host
    yaml = None


def load_workflow_file(path):
    """Parse a workflow, falling back to Ruby when PyYAML is absent.

    These are safety checks, so skipping them on a host without PyYAML would
    quietly remove the protection they provide.
    """
    if yaml is not None:
        return yaml.safe_load(path.read_text())
    script = (
        "require 'yaml'; require 'json'; "
        "puts JSON.generate(YAML.safe_load(File.read(ARGV[0])))"
    )
    result = subprocess.run(["ruby", "-e", script, str(path)],
                            capture_output=True, text=True)
    if result.returncode != 0:
        raise AssertionError(
            f"cannot parse {path.name}: install PyYAML or Ruby.\n" + result.stderr)
    return json.loads(result.stdout)


def load_workflow():
    return load_workflow_file(WORKFLOW_PATH)


class ReleaseWorkflowTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = load_workflow()
        cls.text = WORKFLOW_PATH.read_text()
        # YAML 1.1 parsers read the unquoted key `on:` as a boolean; Ruby's
        # JSON round-trip then renders it as the string "true".
        cls.triggers = next(
            cls.workflow[key] for key in ("on", True, "true") if key in cls.workflow)
        cls.jobs = cls.workflow["jobs"]

    def step(self, job, fragment):
        for candidate in self.jobs[job]["steps"]:
            if fragment.lower() in str(candidate.get("name", "")).lower():
                return candidate
        self.fail(f"no step matching {fragment!r} in job {job}")

    def test_only_manual_dispatch_can_start_a_release(self):
        # A push, tag or pull_request trigger would expose signing credentials
        # to code that no operator approved.
        self.assertEqual(list(self.triggers), ["workflow_dispatch"])
        inputs = self.triggers["workflow_dispatch"]["inputs"]
        self.assertTrue(inputs["version"]["required"])
        self.assertIs(inputs["publish"]["default"], False)

    def test_default_permissions_are_read_only(self):
        self.assertEqual(self.workflow["permissions"], {"contents": "read"})
        self.assertNotIn("permissions", self.jobs["build"])

    def test_write_permission_is_scoped_to_the_publish_job(self):
        self.assertEqual(self.jobs["publish"]["permissions"], {"contents": "write"})
        for job in ("build", "build-windows"):
            self.assertNotIn("permissions", self.jobs[job], job)

    def test_credentialed_jobs_use_the_protected_environment(self):
        for job in ("build", "publish"):
            self.assertEqual(self.jobs[job]["environment"], "release", job)

    def test_build_runs_on_apple_silicon(self):
        self.assertEqual(self.jobs["build"]["runs-on"], "macos-14")

    def test_publication_requires_explicit_opt_in(self):
        self.assertEqual(self.jobs["publish"]["if"], "inputs.publish")
        # Publication waits for BOTH build jobs: an unsigned Windows archive
        # must not attach to a release whose macOS build failed (or vice versa).
        self.assertEqual(self.jobs["publish"]["needs"], ["build", "build-windows"])

    def test_keychain_is_destroyed_on_every_path(self):
        cleanup = self.step("build", "destroy signing keychain")
        self.assertEqual(cleanup["if"], "always()")
        self.assertIn("delete-keychain", cleanup["run"])
        # Secret material written to disk must be removed too.
        self.assertIn("rm -f", cleanup["run"])

    def test_secrets_are_never_echoed(self):
        for secret in ("MACOS_CERTIFICATE_P12", "APPLE_APP_SPECIFIC_PASSWORD",
                       "MACOS_CERTIFICATE_PASSWORD"):
            self.assertNotIn(f"echo $" + secret, self.text)
            self.assertNotIn(f'echo "${secret}"', self.text)
        # The resolved identity fingerprint is masked in logs.
        self.assertIn("::add-mask::", self.text)
        # Passwordless import should notice when password is empty
        self.assertIn("::notice::Using passwordless certificate import", self.text)

    def test_signing_identity_is_resolved_from_the_keychain(self):
        run = self.step("build", "import signing identity")["run"]
        self.assertIn("find-identity -v -p codesigning", run)
        self.assertIn("Developer ID Application:", run)
        # Exactly one identity, so an ambiguous keychain cannot sign silently.
        self.assertIn('[[ "$COUNT" == 1 ]]', run)

    def test_ordering_puts_verification_before_publication(self):
        names = [str(s.get("name", "")).lower() for s in self.jobs["build"]["steps"]]
        order = {key: names.index(next(n for n in names if key in n))
                 for key in ("test gate", "import signing", "build and sign",
                             "notarize", "verify finished archive")}
        self.assertLess(order["test gate"], order["import signing"])
        self.assertLess(order["import signing"], order["build and sign"])
        self.assertLess(order["build and sign"], order["notarize"])
        self.assertLess(order["notarize"], order["verify finished archive"])

    def test_verification_checks_notarization_and_digest(self):
        run = self.step("build", "verify finished archive")["run"]
        self.assertIn("verify-macos-release.py", run)
        self.assertIn("--sha256", run)
        self.assertIn("--notarization-evidence", run)
        # An offline skip would make the gate meaningless in CI.
        self.assertNotIn("--skip-gatekeeper", run)

    def test_release_test_gate_runs_before_credentials(self):
        run = self.step("build", "test gate")["run"]
        for suite in ("test-macos-release-build.py", "test-macos-release-verify.py",
                      "test_render_formula"):
            self.assertIn(suite, run)

    def test_notarization_uses_apple_id_credentials(self):
        run = self.step("build", "notarize")["run"]
        # Check that new Apple ID credentials are used
        self.assertIn("APPLE_ID", run)
        self.assertIn("APPLE_TEAM_ID", run)
        self.assertIn("APPLE_APP_SPECIFIC_PASSWORD", run)
        # Check that old API key credentials are not used
        self.assertNotIn("NOTARY_ISSUER_ID", run)
        self.assertNotIn("NOTARY_KEY_ID", run)
        self.assertNotIn("NOTARY_PRIVATE_KEY", run)
        # Check that notarytool uses the new authentication method
        self.assertIn("--apple-id", run)
        self.assertIn("--team-id", run)
        self.assertIn("--password", run)
        # Check that p8 handling is removed
        self.assertNotIn("notary-key.p8", run)
        self.assertNotIn("base64 --decode", run)

    def test_publish_refuses_to_replace_existing_artifacts(self):
        run = self.step("publish", "refuse to replace")["run"]
        self.assertIn("gh release view", run)
        self.assertIn("exit 1", run)
        # Both platforms' assets are protected: an already-published Windows
        # zip must refuse replacement exactly like the macOS tarball does.
        for asset in ("darwin-arm64.tar.gz", "windows-x64.zip"):
            self.assertIn(asset, run)
        # The published bytes must match the digest the build recorded.
        self.assertIn("shasum -a 256 -c -", run)
        # Digests from BOTH build jobs are verified before publication.
        self.assertIn("needs.build.outputs.sha256", run)
        self.assertIn("needs.build-windows.outputs.sha256", run)

    def test_publish_attaches_both_platforms_artifacts(self):
        run = self.step("publish", "publish release")
        files = run["with"]["files"]
        for asset in ("darwin-arm64.tar.gz", "darwin-arm64.tar.gz.sha256",
                      "windows-x64.zip", "windows-x64.zip.sha256"):
            self.assertIn(asset, files, f"publish must attach {asset}")

    def test_build_windows_job_exists_and_is_manual_only(self):
        job = self.jobs["build-windows"]
        self.assertEqual(job["runs-on"], "windows-latest")
        # The workflow is manual-dispatch only, so no untrusted trigger can
        # start the Windows build either; assert the job carries no `if`
        # that could skip it when publish is false.
        self.assertNotIn("if", job)

    def test_build_windows_job_holds_no_signing_secrets_or_environment(self):
        """The Windows job must never see signing secrets or the release env.

        There is no Windows code signing yet: the artifact is explicitly
        unsigned (docs/windows-artifact.md). Granting it the protected
        `release` environment or any secret would put signing credentials
        on a host that has no need for them.
        """
        job = self.jobs["build-windows"]
        self.assertNotIn("environment", job)
        job_text = json.dumps(job)
        self.assertNotIn("secrets.", job_text)
        # No signing tool may appear anywhere in the job.
        for tool in ("codesign", "signtool", "csc ", "osslsigncode", "notarytool"):
            self.assertNotIn(tool, job_text)
        # And the whole workflow still only touches Apple secrets in the
        # credentialed jobs.
        for secret in ("MACOS_CERTIFICATE_P12", "MACOS_CERTIFICATE_PASSWORD",
                       "APPLE_APP_SPECIFIC_PASSWORD"):
            self.assertNotIn(secret, job_text)

    def test_build_windows_smoke_test_isolates_user_directories(self):
        """The smoke test must not touch real user state on the runner.

        HOME/USERPROFILE/APPDATA/LOCALAPPDATA/TEMP are pointed at throwaway
        paths under RUNNER_TEMP, outside the checkout, before either binary
        runs. Only `--version` is exercised: no agent launch, credentials,
        models or downloads.
        """
        run = self.step("build-windows", "smoke test")["run"]
        for variable in ("HOME", "USERPROFILE", "APPDATA",
                         "LOCALAPPDATA", "TEMP", "TMP"):
            self.assertIn(f'export {variable}="$ISOLATED', run)
        self.assertIn("RUNNER_TEMP", run)
        # Both binaries are exercised with --version only, and their exact
        # output is enforced.
        self.assertIn('AGENT="$ISOLATED/run/cercano-$VERSION-windows-x64/bin/cercano.exe"', run)
        self.assertIn('CLI="$ISOLATED/run/cercano-$VERSION-windows-x64/bin/cercano-cli.exe"', run)
        self.assertIn('"$AGENT" --version', run)
        self.assertIn('"$CLI" --version', run)
        self.assertIn("cercano v$VERSION", run)
        self.assertIn("cercano-cli v$VERSION", run)
        # A failed smoke test must fail the job, and stray writes are a
        # failure too.
        self.assertIn("exit 1", run)
        self.assertIn("refusing", run)
        # The smoke test runs the exact archived binaries, extracted to the
        # isolated directory, not the repo working tree.
        self.assertIn("python -m zipfile -e", run)
        self.assertIn("$ISOLATED/run", run)

    def test_build_windows_tests_gates_before_packaging(self):
        """The release test gate must precede the packaging step."""
        names = [str(s.get("name", "")).lower()
                 for s in self.jobs["build-windows"]["steps"]]
        gate = names.index("run release test gate")
        build = names.index("build unsigned windows archive")
        smoke = names.index("smoke test binaries with isolated user directories")
        self.assertLess(gate, build)
        self.assertLess(build, smoke)

    def test_build_windows_gate_covers_both_suites(self):
        run = self.step("build-windows", "test gate")["run"]
        self.assertIn("test-windows-release-build.py", run)
        self.assertIn("test-release-workflow.py", run)

    def test_build_windows_runs_native_helper_tests(self):
        """Available Windows-native tests must run in the job."""
        run = self.step("build-windows", "native helper tests")["run"]
        self.assertIn("go test", run)
        self.assertIn("./internal/localruntime/llamaserver", run)

    def test_version_input_is_validated_against_a_real_tag(self):
        run = self.step("build", "validate inputs")["run"]
        self.assertIn("refs/tags/v$VERSION", run)
        self.assertIn("[1-9][0-9]*", run)

    def test_validation_rejects_a_tag_on_another_commit(self):
        script = self.step("build", "validate inputs")["run"]
        with tempfile.TemporaryDirectory() as directory:
            def git(*args):
                return subprocess.run(["git", *args], cwd=directory, check=True,
                                      capture_output=True, text=True)
            git("init")
            git("config", "user.name", "Release Test")
            git("config", "user.email", "test@example.invalid")
            git("commit", "--allow-empty", "-m", "tagged source")
            git("tag", "-a", "v1.2.3", "-m", "release fixture")
            def validate():
                return subprocess.run(["bash", "-c", 'VERSION=1.2.3\n' + script],
                                      cwd=directory, capture_output=True, text=True)
            self.assertEqual(validate().returncode, 0)
            git("commit", "--allow-empty", "-m", "different source")
            result = validate()
            self.assertNotEqual(result.returncode, 0,
                                "a tag on another commit must not label this checkout")
            self.assertIn("does not match", result.stdout + result.stderr)

    def test_certificate_preflight_executes_with_empty_password(self):
        script = self.step("build", "import signing identity")["run"]
        # Execute only validation, stopping before random password generation
        # and every real Keychain operation. No signing credentials are used.
        preflight = script.split('KEYCHAIN_PASSWORD="$(openssl', 1)[0]
        self.assertNotIn('security ', preflight)
        for certificate, password, allowed in [
                ("", "", False), ("inert-fixture", "", True),
                ("inert-fixture", "fixture-password", True)]:
            result = subprocess.run(
                ["bash", "-c", preflight + '\necho PREFLIGHT_OK\n'],
                env={"CERTIFICATE_P12": certificate,
                     "CERTIFICATE_PASSWORD": password},
                capture_output=True, text=True)
            self.assertEqual(result.returncode == 0, allowed, result.stderr)
            self.assertEqual("PREFLIGHT_OK" in result.stdout, allowed)

    def test_passwordless_certificate_import_is_supported(self):
        """Test that empty MACOS_CERTIFICATE_PASSWORD is allowed with valid P12."""
        import_step = self.step("build", "import signing identity")
        run_script = import_step["run"]
        
        # Check that only CERTIFICATE_P12 is required
        self.assertIn('[[ -n "$CERTIFICATE_P12" ]]', run_script)
        # Check that empty password is allowed
        self.assertIn('[[ -z "$CERTIFICATE_PASSWORD" ]]', run_script)
        # Check that notice is shown for passwordless import
        self.assertIn('echo "::notice::Using passwordless certificate import"', run_script)
        # Check that security import still uses the password variable (which may be empty)
        self.assertIn('security import "$CERT_PATH" -k "$KEYCHAIN_PATH" -P "$CERTIFICATE_PASSWORD"', run_script)

    def test_concurrency_prevents_overlapping_runs_for_a_version(self):
        concurrency = self.workflow["concurrency"]
        self.assertIn("inputs.version", concurrency["group"])
        self.assertIs(concurrency["cancel-in-progress"], False)

    def test_shell_snippets_parse(self):
        for job, spec in self.jobs.items():
            for step in spec["steps"]:
                script = step.get("run")
                if not script:
                    continue
                name = step.get("name", "unnamed")
                result = subprocess.run(["bash", "-n"], input=script,
                                        capture_output=True, text=True)
                self.assertEqual(result.returncode, 0,
                                 f"{job}/{name} shell syntax error: {result.stderr}")


class NoAutomaticPublishingTest(unittest.TestCase):
    """No workflow may publish a release without an explicit operator run.

    The removed release.yml triggered on every `v*` tag and attached unsigned,
    cross-compiled binaries to a GitHub Release. Since the signed pipeline
    requires that same tag to exist, tagging a version would have raced it and
    published unsigned artifacts. Releases are now operator-triggered only.
    """

    def test_legacy_tag_triggered_workflow_is_gone(self):
        self.assertFalse((REPO / ".github" / "workflows" / "release.yml").exists(),
                         "release.yml publishes unsigned binaries on tag push and must stay removed")

    def test_no_workflow_publishes_on_push_or_tag(self):
        for path in sorted((REPO / ".github" / "workflows").glob("*.yml")):
            workflow = load_workflow_file(path)
            triggers = next((workflow[key] for key in ("on", True, "true")
                             if key in workflow), {})
            publishes = "action-gh-release" in path.read_text() or "gh release create" in path.read_text()
            if not publishes:
                continue
            self.assertNotIn("push", triggers,
                             f"{path.name} publishes releases and must not trigger on push")
            self.assertNotIn("pull_request", triggers,
                             f"{path.name} publishes releases and must not trigger on pull_request")


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
