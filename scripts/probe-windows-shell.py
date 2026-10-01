"""Credential-free diagnostic for native Python shell resolution on Windows."""
import os
from pathlib import Path
import shutil
import subprocess

print('PATH-selected bash:', shutil.which('bash'), flush=True)
print('Windows system launcher exists:', Path(r'C:\Windows\System32\bash.exe').exists(), flush=True)
subprocess.run(['where.exe', 'bash'], check=False)
shells = ['bash', r'C:\Program Files\Git\bin\bash.exe', r'C:\Program Files\Git\usr\bin\bash.exe']
for shell in shells:
    if shell != 'bash' and not Path(shell).is_file():
        continue
    for minimal in (False, True):
        env = {} if minimal else dict(os.environ)
        env.update(CERTIFICATE_P12='inert-fixture', CERTIFICATE_PASSWORD='')
        cases = [(['-c', 'printf "SHELL_OK\\n"'], None),
                 (['-n'], 'set -euo pipefail\n[[ -n "$CERTIFICATE_P12" ]]\n'),
                 (['-c', 'set -euo pipefail\n[[ -n "$CERTIFICATE_P12" ]] || exit 1\nprintf "PREFLIGHT_OK\\n"'], None)]
        for args, stdin in cases:
            try:
                r = subprocess.run([shell, *args], input=stdin, env=env,
                                   capture_output=True, timeout=20)
                print(repr({'shell': shell, 'minimal_env': minimal, 'args': args,
                            'exit': r.returncode, 'stdout': r.stdout[:2000],
                            'stderr': r.stderr[:2000]}), flush=True)
            except (OSError, subprocess.TimeoutExpired) as e:
                print(type(e).__name__, str(e), flush=True)
