import os
import subprocess
import sys

# Set environment variables for testing
os.environ['SUBMIT'] = 'bad json'
os.environ['SUBMIT_STDERR'] = 'APPLE_APP_SPECIFIC_PASSWORD=abc123 MACOS_CERTIFICATE_PASSWORD=secret123 CERTIFICATE_PASSWORD=pwd123 MACOS_CERTIFICATE_P12=p12file.pem CERTIFICATE_P2=pem-content\n-----BEGIN PRIVATE KEY-----\nsecretkeydata\n-----END PRIVATE KEY-----'
os.environ['SUBMIT_EXIT'] = '1'

# Run the script with minimal arguments
result = subprocess.run(['python3', './scripts/notarize-macos-local.py', '/tmp', '/tmp/test_output', '--keychain-profile', 'test'], 
                       capture_output=True, text=True)

print('STDOUT:', result.stdout)
print('STDERR:', result.stderr)
print('RETURN CODE:', result.returncode)