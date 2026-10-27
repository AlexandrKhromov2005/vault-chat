"""Email opt-in must preserve legacy deployments and refuse incompatible images."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import tarfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class EmailDeployment(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='vault-email-ops-')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.bindir = self.root / 'bin'
        self.bindir.mkdir(mode=0o700)
        (self.root / '.env.vps').write_text('VAULT_IMAGE_TAG=synthetic-current\nPUBLIC_IP=198.51.100.10\n')
        self.trace = self.root / 'trace'
        self.environment = {'PATH': str(self.bindir) + ':' + os.environ['PATH'],
                            'HOME': str(self.root), 'VAULT_ROOT': str(self.root),
                            'VAULT_PROJECT': 'vault-email-fixture', 'TRACE': str(self.trace)}
        self.executable('id', '#!/bin/sh\necho 0\n')
        self.executable('docker', '''#!/usr/bin/env python3
import json,os,sys
with open(os.environ['TRACE'],'a') as trace: trace.write(json.dumps(sys.argv[1:])+'\\n')
if sys.argv[1:3] == ['image','inspect']: sys.exit(0)
if sys.argv[1:3] == ['volume','ls']: sys.exit(0)
if sys.argv[1:3] == ['volume','inspect']:
    from pathlib import Path
    if Path(os.environ['VAULT_ROOT'],'minio-created').exists() and sys.argv[3].endswith('_miniodata'):
        print(str(Path(os.environ['VAULT_ROOT'],'objects'))); sys.exit(0)
    sys.exit(1)
if '-check-account-email' in sys.argv and os.environ.get('EMAIL_IMAGE_SUPPORTED')!='1': sys.exit(42)
if 'compose' in sys.argv and 'create' in sys.argv:
    from pathlib import Path
    Path(os.environ['VAULT_ROOT'],'minio-created').touch()
if sys.argv[1:2] == ['compose']:
    from pathlib import Path
    values={}
    for index,arg in enumerate(sys.argv[:-1]):
        if arg=='--env-file':
            for line in Path(sys.argv[index+1]).read_text().splitlines():
                if line.startswith('VAULT_IMAGE_TAG='): values['VAULT_IMAGE_TAG']=line.split('=',1)[1]
    Path(os.environ['VAULT_ROOT'],'selected-tag').write_text(os.environ.get('VAULT_IMAGE_TAG',values.get('VAULT_IMAGE_TAG','')))
sys.exit(0)
''')

    def executable(self, name, content):
        path = self.bindir / name
        path.write_text(content)
        path.chmod(0o700)
        return path

    def shell(self, command):
        return subprocess.run(['sh', '-eu', '-c', '. "$1"; ' + command,
                               'fixture', str(ROOT / 'scripts/vps/common.sh')],
                              env=self.environment, capture_output=True, text=True, timeout=10)

    def calls(self):
        return [json.loads(line) for line in self.trace.read_text().splitlines()] if self.trace.exists() else []

    def test_email_opt_in_rejects_old_image_before_update_mutation(self):
        (self.root / '.env.email').write_text('AUTH_PUBLIC_URL=https://vault.example.invalid\n')
        helper = self.root / 'scripts/vps.sh'
        helper.parent.mkdir()
        helper.write_text('#!/bin/sh\ntouch "$VAULT_ROOT/backup-started"\n')
        helper.chmod(0o700)
        original = (self.root / '.env.vps').read_bytes()
        result = subprocess.run(['sh', str(ROOT / 'scripts/vps/update.sh'), 'synthetic-legacy'],
                                env=self.environment, capture_output=True, text=True, timeout=10)
        self.assertEqual((self.root / '.env.vps').read_bytes(), original,
                         'a legacy image silently ignored email config and changed the image selector')
        self.assertFalse((self.root / 'backup-started').exists(), 'guard must run before backup/pause')
        self.assertNotEqual(result.returncode, 0)
        guard = next(args for args in self.calls() if '-check-account-email' in args)
        self.assertIn('none', guard)
        self.assertIn('vault-chat/auth:synthetic-legacy', guard)
        self.assertNotIn('--mount', guard)
        self.assertNotIn('-e', guard)

    def test_disabled_legacy_path_needs_no_email_files_or_guard(self):
        result = self.shell('check_rpc_images synthetic-old; compose config --quiet')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any('-check-account-email' in args for args in self.calls()))
        self.assertFalse(any('docker-compose.email.yml' in ' '.join(args) for args in self.calls()))

    def mail_key_marker(self):
        directory = self.root / 'secrets/mail'
        directory.mkdir(parents=True, mode=0o700)
        key = directory / 'outbox.key'
        key.write_bytes(b'synthetic-key-fixture')
        key.chmod(0o400)

    def test_disabled_delivery_key_marker_still_rejects_legacy_up(self):
        self.mail_key_marker()
        result = subprocess.run(['sh', str(ROOT / 'scripts/vps.sh'), 'up'], env=self.environment,
                                capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0, 'delivery off must not permit an image that ignores stored ownership policy')
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))
        self.assertFalse(any(args[0] == 'compose' for args in self.calls()))

    def test_disabled_delivery_key_marker_still_rejects_legacy_update(self):
        self.mail_key_marker()
        helper = self.root / 'scripts/vps.sh'
        helper.parent.mkdir()
        helper.write_text('#!/bin/sh\ntouch "$VAULT_ROOT/backup-started"\n')
        helper.chmod(0o700)
        original = (self.root / '.env.vps').read_bytes()
        result = subprocess.run(['sh', str(ROOT / 'scripts/vps/update.sh'), 'synthetic-legacy'],
                                env=self.environment, capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / '.env.vps').read_bytes(), original)
        self.assertFalse((self.root / 'backup-started').exists())
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))

    def test_disabled_delivery_keeps_selector_pinned_without_overlay(self):
        self.mail_key_marker()
        self.environment.update(EMAIL_IMAGE_SUPPORTED='1', VAULT_IMAGE_TAG='synthetic-environment-legacy')
        result = self.shell('check_rpc_images; compose config --quiet')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))
        self.assertEqual((self.root / 'selected-tag').read_text(), 'synthetic-current')
        self.assertFalse(any('docker-compose.email.yml' in ' '.join(args) for args in self.calls()))

    def test_false_delivery_setting_still_requires_email_aware_image(self):
        (self.root / '.env.email').write_text('AUTH_MAIL_ENABLED=false\nAUTH_EMAIL_LEGACY_DEADLINE=2030-01-01T00:00:00Z\n')
        result = self.shell('check_rpc_images synthetic-legacy')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))

    def test_install_rejects_old_email_image_before_host_configuration(self):
        (self.root / '.env.email').write_text('AUTH_PUBLIC_URL=https://vault.example.invalid\n')
        (self.root / '.env.vps').write_text('VAULT_IMAGE_TAG=synthetic-legacy\nPUBLIC_IP=8.8.8.8\n')
        installer = self.root / 'installer'
        installer.mkdir()
        (installer / 'common.sh').write_bytes((ROOT / 'scripts/vps/common.sh').read_bytes())
        script = (ROOT / 'scripts/vps/install.sh').read_text()
        # Simulate the installer's host prerequisites in this private fixture.
        # All operations commands remain the actual production shell code.
        script = script.replace('[ "$VAULT_ROOT" = /opt/vault-chat ]',
                                '[ "$VAULT_ROOT" = "' + str(self.root) + '" ]')
        script = script.replace('. /etc/os-release', 'ID=ubuntu; VERSION_ID=24.04')
        (installer / 'install.sh').write_text(script)
        self.executable('apt-get', '#!/bin/sh\ntouch "$VAULT_ROOT/host-mutated"\nexit 99\n')
        result = subprocess.run(['sh', str(installer / 'install.sh')], env=self.environment,
                                capture_output=True, text=True, timeout=10)
        self.assertFalse((self.root / 'host-mutated').exists(), 'old email image reached host mutation')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))

    def test_opt_in_composes_overlay_and_keeps_rpc_guard(self):
        (self.root / '.env.email').write_text('AUTH_PUBLIC_URL=https://vault.example.invalid\n')
        (self.root / '.env.rpc').write_text('RPC_TLS_MODE=mtls\n')
        self.environment['EMAIL_IMAGE_SUPPORTED'] = '1'
        result = self.shell('check_rpc_images synthetic-mail; compose config --quiet')
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertIn('-check-account-email', calls[0])
        self.assertEqual(sum('-check-rpc-tls' in args for args in calls), 4)
        composed = next(args for args in calls if args[0] == 'compose')
        self.assertIn(str(self.root / '.env.email'), composed)
        self.assertIn(str(self.root / 'deployments/docker-compose.email.yml'), composed)

    def test_guard_and_compose_use_the_same_image_selector(self):
        (self.root / '.env.email').write_text('VAULT_IMAGE_TAG=synthetic-file-legacy\n')
        self.environment.update(EMAIL_IMAGE_SUPPORTED='1', VAULT_IMAGE_TAG='synthetic-environment-legacy')
        result = self.shell('check_rpc_images; compose config --quiet')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('vault-chat/auth:synthetic-current', self.calls()[0])
        self.assertEqual((self.root / 'selected-tag').read_text(), 'synthetic-current',
                         'ambient/overlay selectors must not retarget Compose after a different image passed the guard')

    @unittest.skipUnless(shutil.which('docker'), 'Docker Compose CLI required for read-only configuration')
    def test_overlay_reaches_smtp_only_from_auth_and_keeps_credentials_in_files(self):
        values = {
            'PUBLIC_IP': '198.51.100.10', 'VAULT_IMAGE_TAG': 'synthetic',
            'AUTH_DB_PASSWORD': 'fixture', 'CHAT_DB_PASSWORD': 'fixture', 'MEDIA_DB_PASSWORD': 'fixture',
            'AUTH_RUNTIME_DB_PASSWORD': 'fixture', 'CHAT_RUNTIME_DB_PASSWORD': 'fixture',
            'MEDIA_RUNTIME_DB_PASSWORD': 'fixture', 'REDIS_PASSWORD': 'fixture',
            'S3_ACCESS_KEY': 'fixture', 'S3_SECRET_KEY': 'fixture',
            'S3_ROOT_ACCESS_KEY': 'fixture-root', 'S3_ROOT_SECRET_KEY': 'fixture',
            'AUTH_PUBLIC_URL': 'https://vault.example.invalid', 'AUTH_SMTP_ADDRESS': 'smtp.example.invalid:587',
            'AUTH_SMTP_FROM': 'Vault <sender@example.invalid>', 'AUTH_SMTP_USERNAME': 'fixture-user',
        }
        envfile = self.root / 'synthetic.env'
        envfile.write_text(''.join(key + '=' + value + '\n' for key, value in values.items()))
        environment = {'PATH': os.environ['PATH'], 'HOME': str(self.root),
                       'DOCKER_CONFIG': str(self.root / 'docker-config')}
        result = subprocess.run(['docker', 'compose', '--env-file', str(envfile),
                                 '-f', str(ROOT / 'deployments/docker-compose.vps.yml'),
                                 '-f', str(ROOT / 'deployments/docker-compose.email.yml'),
                                 'config', '--format', 'json'], env=environment,
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        config = json.loads(result.stdout)
        auth = config['services']['auth']
        env = auth['environment']
        self.assertEqual(env['AUTH_MAIL_ENABLED'], 'true')
        self.assertEqual(env['AUTH_SMTP_TLS_MODE'], 'starttls')
        self.assertEqual(env['AUTH_MAIL_KEY_FILE'], '/run/secrets/mail/outbox.key')
        self.assertEqual(env['AUTH_SMTP_PASSWORD_FILE'], '/run/secrets/mail/smtp_password')
        self.assertNotIn('AUTH_SMTP_PASSWORD', env)
        mounts = [v for v in auth['volumes'] if v['target'] == '/run/secrets/mail']
        self.assertEqual(len(mounts), 1)
        self.assertTrue(mounts[0]['source'].endswith('/secrets/mail'))
        self.assertTrue(mounts[0]['read_only'])
        self.assertFalse(mounts[0]['bind'].get('create_host_path', False))
        egress = [name for name in auth['networks'] if not config['networks'][name].get('internal', False)]
        self.assertTrue(egress, 'SMTP delivery requires outbound connectivity from Auth')
        for name, other in config['services'].items():
            if name == 'auth':
                continue
            self.assertFalse(set(egress).intersection(other.get('networks', {})), name)
            self.assertFalse(any(v['target'] == '/run/secrets/mail' for v in other.get('volumes', [])), name)
            self.assertNotIn('AUTH_MAIL_ENABLED', other.get('environment', {}), name)
        self.assertTrue({'rpc', 'auth-data', 'events'}.issubset(auth['networks']))
        self.assertFalse(auth.get('ports'))
        self.assertEqual(set(auth['networks']), {'rpc', 'auth-data', 'events', 'mail-egress'})
        # The same opt-in must augment, not replace, the development networks.
        dev_command = ['docker', 'compose', '--env-file', str(envfile),
                       '-f', str(ROOT / 'deployments/docker-compose.yml'),
                       '-f', str(ROOT / 'deployments/docker-compose.email.yml'),
                       'config', '--format', 'json']
        result = subprocess.run(dev_command, env=environment, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        dev = json.loads(result.stdout)
        self.assertEqual(set(dev['services']['auth']['networks']), {'rpc', 'auth-data', 'events', 'mail-egress'})
        self.assertFalse(dev['services']['auth'].get('ports'))
        for name, other in dev['services'].items():
            if name != 'auth':
                self.assertNotIn('mail-egress', other.get('networks', {}), name)
        # Explicit empty credentials permit a trusted relay without AUTH;
        # custom CAs and implicit TLS still stay inside Auth's configuration.
        with envfile.open('a') as stream:
            stream.write('AUTH_SMTP_TLS_MODE=tls\nAUTH_SMTP_USERNAME=\nAUTH_SMTP_PASSWORD_FILE=\n'
                         'AUTH_SMTP_CA_FILE=/run/secrets/mail/ca.pem\n')
        result = subprocess.run(dev_command, env=environment, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        enabled = json.loads(result.stdout)['services']['auth']['environment']
        self.assertEqual(enabled['AUTH_SMTP_TLS_MODE'], 'tls')
        self.assertEqual(enabled['AUTH_SMTP_PASSWORD_FILE'], '')
        self.assertEqual(enabled['AUTH_SMTP_USERNAME'], '')
        self.assertEqual(enabled['AUTH_SMTP_CA_FILE'], '/run/secrets/mail/ca.pem')
        with envfile.open('a') as stream:
            stream.write('AUTH_MAIL_ENABLED=false\nAUTH_EMAIL_LEGACY_DEADLINE=2030-01-01T00:00:00Z\n')
        result = subprocess.run(dev_command, env=environment, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        maintenance = json.loads(result.stdout)['services']['auth']['environment']
        self.assertEqual(maintenance['AUTH_MAIL_ENABLED'], 'false')
        self.assertEqual(maintenance['AUTH_EMAIL_LEGACY_DEADLINE'], '2030-01-01T00:00:00Z')

    def make_backup(self, dot_mail=False, delivery=True, dot_key=False):
        backup = self.root / 'backup'
        backup.mkdir()
        members = {
            '.env.vps': b'VAULT_IMAGE_TAG=synthetic-mail\nPUBLIC_IP=198.51.100.10\n',
            '.env.email': b'AUTH_PUBLIC_URL=https://vault.example.invalid\n',
            'deployments/docker-compose.vps.yml': b'services: {}\n',
            'deployments/docker-compose.email.yml': b'services: {}\n',
            'secrets/auth_jwt_private.pem': b'synthetic-private-fixture',
            'secrets/auth_jwt_public.pem': b'synthetic-public-fixture',
            'secrets/tls/cert': b'synthetic-tls-fixture',
            'secrets/mail/outbox.key': b'synthetic-key-fixture',
            'secrets/mail/smtp_password': b'synthetic-password-fixture',
        }
        if dot_mail:
            members['./.env.email'] = members.pop('.env.email')
        if not delivery:
            members.pop('.env.email')
            members.pop('deployments/docker-compose.email.yml')
        if dot_key:
            members['./secrets/mail/outbox.key'] = members.pop('secrets/mail/outbox.key')
        with tarfile.open(backup / 'config.tar.gz', 'w:gz') as archive:
            for name, data in members.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                member.mode = 0o644  # Restore must repair permissions and ownership.
                archive.addfile(member, io.BytesIO(data))
        with tarfile.open(backup / 'media-objects.tar.gz', 'w:gz'):
            pass
        for name in ('auth.dump', 'chat.dump', 'media.dump'):
            (backup / name).write_bytes(b'synthetic dump')
        (backup / 'images.txt').write_text('vault-chat/auth:synthetic-mail\n')
        names = ('auth.dump', 'chat.dump', 'media.dump', 'config.tar.gz', 'media-objects.tar.gz', 'images.txt')
        (backup / 'SHA256SUMS').write_text(''.join(
            hashlib.sha256((backup / name).read_bytes()).hexdigest() + '  ' + name + '\n' for name in sorted(names)))
        (backup / 'COMPLETE').write_text('1\n')
        return backup

    def restore_fixture(self, supported, dot_mail=False, delivery=True, dot_key=False):
        backup = self.make_backup(dot_mail, delivery, dot_key)
        target = self.root / 'restored'
        helpers = target / 'scripts/vps'
        helpers.mkdir(parents=True)
        (helpers / 'archive.py').write_bytes((ROOT / 'scripts/vps/archive.py').read_bytes())
        (target / 'objects').mkdir(mode=0o700)
        self.environment['VAULT_ROOT'] = str(target)
        if supported:
            self.environment['EMAIL_IMAGE_SUPPORTED'] = '1'
        self.executable('chown', '''#!/usr/bin/env python3
import json,os,sys
with open(os.environ['TRACE'],'a') as trace: trace.write(json.dumps(['chown',*sys.argv[1:]])+'\\n')
''')
        result = subprocess.run(['sh', str(ROOT / 'scripts/vps/restore.sh'), str(backup)],
                                env=self.environment, capture_output=True, text=True, timeout=15)
        return result, target

    def test_mail_backup_is_accepted_and_restore_repairs_private_file_ownership(self):
        result, target = self.restore_fixture(True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((target / 'secrets/mail/outbox.key').stat().st_mode & 0o777, 0o400)
        self.assertEqual((target / 'secrets/mail/smtp_password').stat().st_mode & 0o777, 0o400)
        self.assertEqual((target / 'secrets/mail').stat().st_mode & 0o777, 0o700)
        self.assertEqual((target / '.env.email').stat().st_mode & 0o777, 0o600)
        self.assertIn(['chown', '-R', '65532:65532', str(target / 'secrets/mail')], self.calls())

    def test_restore_rejects_old_email_image_before_extracting_live_configuration(self):
        result, target = self.restore_fixture(False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((target / '.env.vps').exists(), 'legacy image guard must precede live config extraction')
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))
        self.assertFalse(any(args[0] == 'compose' for args in self.calls()))

    def test_restore_guard_recognizes_normalized_archive_paths(self):
        result, target = self.restore_fixture(False, dot_mail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((target / '.env.vps').exists(), './.env.email must not bypass the pre-extraction guard')
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))

    def test_disabled_delivery_backup_key_marker_rejects_legacy_restore(self):
        result, target = self.restore_fixture(False, delivery=False, dot_key=True)
        self.assertNotEqual(result.returncode, 0, 'restored historical mail key must preserve the image compatibility floor')
        self.assertFalse((target / '.env.vps').exists())
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))
        self.assertFalse(any(args[0] == 'compose' for args in self.calls()))

    def test_disabled_delivery_restore_keeps_private_mail_key_and_guard(self):
        result, target = self.restore_fixture(True, delivery=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((target / '.env.email').exists())
        self.assertEqual((target / 'secrets/mail/outbox.key').stat().st_mode & 0o777, 0o400)
        self.assertIn(['chown', '-R', '65532:65532', str(target / 'secrets/mail')], self.calls())
        self.assertTrue(any('-check-account-email' in args for args in self.calls()))

    def test_restore_does_not_overwrite_an_existing_email_opt_in(self):
        backup = self.make_backup()
        target = self.root / 'restored'
        target.mkdir()
        helpers = target / 'scripts/vps'
        helpers.mkdir(parents=True)
        (helpers / 'archive.py').write_bytes((ROOT / 'scripts/vps/archive.py').read_bytes())
        (target / 'objects').mkdir(mode=0o700)
        self.executable('chown', '#!/bin/sh\nexit 0\n')
        (target / '.env.email').write_text('AUTH_PUBLIC_URL=https://existing.example.invalid\n')
        self.environment['VAULT_ROOT'] = str(target)
        self.environment['EMAIL_IMAGE_SUPPORTED'] = '1'
        result = subprocess.run(['sh', str(ROOT / 'scripts/vps/restore.sh'), str(backup)],
                                env=self.environment, capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('fresh checkout', result.stderr)
        self.assertEqual((target / '.env.email').read_text(), 'AUTH_PUBLIC_URL=https://existing.example.invalid\n')
        self.assertEqual(self.calls(), [])

    def test_backup_configuration_stream_preserves_mail_opt_in(self):
        self.backup_configuration_fixture(delivery=True)

    def test_disabled_delivery_backup_configuration_keeps_key_marker(self):
        self.backup_configuration_fixture(delivery=False)

    def backup_configuration_fixture(self, delivery):
        helpers = self.root / 'scripts/vps'
        helpers.mkdir(parents=True)
        (helpers / 'backup_config.py').write_text('pass\n')
        (helpers / 'encrypted_backup.py').write_text('''import os,pathlib,subprocess,sys
if pathlib.Path(sys.argv[2]).name == 'config.tar.gz.age':
    pathlib.Path(os.environ['VAULT_ROOT'],'captured-config.tar.gz').write_bytes(subprocess.check_output(sys.argv[3:]))
pathlib.Path(sys.argv[2]).write_bytes(b'synthetic encrypted output')
''')
        self.executable('age', '#!/bin/sh\ncat >/dev/null\n')
        self.executable('rclone', '#!/bin/sh\nexit 0\n')
        (self.root / '.env.vps').write_text('VAULT_IMAGE_TAG=synthetic-mail\nBACKUP_AGE_RECIPIENT=synthetic-recipient\nBACKUP_REMOTE=synthetic:offhost\n')
        if delivery:
            (self.root / '.env.email').write_text('AUTH_PUBLIC_URL=https://vault.example.invalid\n')
        (self.root / 'secrets/mail').mkdir(parents=True, mode=0o700)
        (self.root / 'secrets/mail/outbox.key').write_bytes(b'synthetic-key-fixture')
        (self.root / 'deployments').mkdir()
        for name in ('docker-compose.vps.yml', 'docker-compose.email.yml'):
            (self.root / 'deployments' / name).write_text('services: {}\n')
        (self.root / 'minio-created').touch()
        self.environment['VAULT_BACKUP_ROOT'] = str(self.root / 'backups')
        result = subprocess.run(['sh', str(ROOT / 'scripts/vps/backup.sh')], env=self.environment,
                                capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        with tarfile.open(self.root / 'captured-config.tar.gz') as archive:
            names = set(archive.getnames())
        if delivery:
            self.assertIn('.env.email', names)
            self.assertIn('deployments/docker-compose.email.yml', names)
        else:
            self.assertNotIn('.env.email', names)
        self.assertIn('secrets/mail/outbox.key', names)


if __name__ == '__main__':
    unittest.main()
