"""Stream a producer into age without writing plaintext or hiding pipe errors."""
from pathlib import Path
import subprocess
import sys


def encrypt(recipient, destination, command):
    producer = subprocess.Popen(command, stdout=subprocess.PIPE)
    consumer = None
    try:
        consumer = subprocess.Popen(['age', '--recipient', recipient, '--output', destination], stdin=producer.stdout)
        producer.stdout.close()
        encrypted = consumer.wait()
        if encrypted:
            producer.terminate()
        produced = producer.wait()
        if produced or encrypted:
            Path(destination).unlink(missing_ok=True)
            return max(1, produced if produced > 0 else encrypted)
        return 0
    finally:
        for process in (producer, consumer):
            if process is not None and process.poll() is None:
                process.terminate()
                process.wait()


if __name__ == '__main__':
    if len(sys.argv) < 4:
        sys.exit('Usage: encrypted_backup.py AGE_RECIPIENT OUTPUT COMMAND...')
    sys.exit(encrypt(sys.argv[1], sys.argv[2], sys.argv[3:]))
