import os
import subprocess
import tempfile
import shutil
import json

# Create a temporary directory with proper binaries
temp = tempfile.mkdtemp()
os.makedirs(os.path.join(temp, 'stage bin'), exist_ok=True)

# Create mock binaries that pass the basic checks
with open(os.path.join(temp, 'stage bin', 'cercano'), 'wb') as f:
    f.write(b'mock executable')
with open(os.path.join(temp, 'stage bin', 'cercano-cli'), 'wb') as f:
    f.write(b'mock executable')

os.chmod(os.path.join(temp, 'stage bin', 'cercano'), 0o755)
os.chmod(os.path.join(temp, 'stage bin', 'cercano-cli'), 0o755)

# Set environment variables for testing
os.environ['SUBMIT'] = 'bad json'
os.environ['SUBMIT_STDERR'] = 'APPLE_APP_SPECIFIC_PASSWORD=abc123 MACOS_CERTIFICATE_PASSWORD=secret123 CERTIFICATE_PASSWORD=pwd123 MACOS_CERTIFICATE_P12=p12file.pem CERTIFICATE_P2=pem-content\n-----BEGIN PRIVATE KEY-----\nsecretkeydata\n-----END PRIVATE KEY-----'
os.environ['SUBMIT_EXIT'] = '1'

output_dir = '/tmp/test_output_' + str(os.getpid())

try:
    result = subprocess.run(['python3', './scripts/notarize-macos-local.py', 
                           os.path.join(temp, 'stage bin'), output_dir, 
                           '--keychain-profile', 'test'], 
                          capture_output=True, text=True)
    
    print('STDOUT:', result.stdout)
    print('STDERR:', result.stderr)
    print('RETURN CODE:', result.returncode)
    
    # Check if diagnostic-report.json was created
    if os.path.exists(output_dir):
        print(f'Output directory exists: {output_dir}')
        if os.path.exists(os.path.join(output_dir, 'diagnostic-report.json')):
            print('diagnostic-report.json exists')
            with open(os.path.join(output_dir, 'diagnostic-report.json'), 'r') as f:
                content = f.read()
                print('diagnostic-report.json content:', content)
                # Check for redaction
                if '[REDACTED]' in content:
                    print('REDACTION FOUND')
                else:
                    print('NO REDACTION FOUND')
        else:
            print('diagnostic-report.json does not exist')
            files = os.listdir(output_dir)
            print('Files in output directory:', files)
    else:
        print('Output directory does not exist')

finally:
    shutil.rmtree(temp, ignore_errors=True)
    shutil.rmtree(output_dir, ignore_errors=True)