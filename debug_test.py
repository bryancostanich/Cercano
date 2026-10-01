import os
import subprocess
import tempfile
import shutil

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
os.environ['SUBMIT_EXIT'] = '1'
os.environ['SUBMIT_STDERR'] = 'test error'

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
                print('diagnostic-report.json content:', f.read())
        else:
            print('diagnostic-report.json does not exist')
            files = os.listdir(output_dir)
            print('Files in output directory:', files)
    else:
        print('Output directory does not exist')

finally:
    shutil.rmtree(temp, ignore_errors=True)
    shutil.rmtree(output_dir, ignore_errors=True)