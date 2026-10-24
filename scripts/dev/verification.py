"""Commit-bound local CI evidence; not a sandbox or a trusted attestation service."""
import hashlib
import fcntl
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).resolve().parent))
from security import build_network, inventory, output  # noqa: E402


def git(root, *args):
    return output(['git', '-C', str(root), *args])


def clean(root):
    if git(root, 'status', '--porcelain', '--untracked-files=all'):
        raise ValueError('dirty checkout: commit the changes before xmake ci; use xmake test while editing')


def receipt_path(root, sha):
    if len(sha) != 40 or any(c not in '0123456789abcdef' for c in sha):
        raise ValueError('full commit SHA required')
    common = Path(git(root, 'rev-parse', '--path-format=absolute', '--git-common-dir'))
    return common / 'vault-verification' / (sha + '.json')


def require_receipt(root, sha, tools):
    try:
        receipt = json.loads(receipt_path(root, sha).read_text())
    except (OSError, ValueError) as error:
        raise ValueError('no successful verification for this commit; run xmake ci') from error
    if receipt.get('commit') != sha or receipt.get('tree') != git(root, 'rev-parse', sha + '^{tree}'):
        raise ValueError('verification does not match this source tree')
    if receipt.get('tools') != tools:
        raise ValueError('verification tools changed; rerun xmake ci')
    if not 0 <= time.time() - receipt.get('completed_at', 0) < 24 * 3600:
        raise ValueError('verification expired (24 hours); rerun xmake ci')
    try:
        digest = hashlib.sha256(Path(receipt['log']).read_bytes()).hexdigest()
    except (KeyError, OSError) as error:
        raise ValueError('verification log missing; rerun xmake ci') from error
    if digest != receipt.get('log_sha256'):
        raise ValueError('verification log changed; rerun xmake ci')
    return receipt


def check_environment():
    # Do not inherit API tokens, production configuration, test URLs, or Go overrides.
    allowed = {'PATH', 'HOME', 'USER', 'LANG', 'LC_ALL', 'TMPDIR', 'XDG_CACHE_HOME', 'XDG_RUNTIME_DIR', 'DOCKER_CONTEXT', 'TERM'}
    env = {key: value for key, value in os.environ.items() if key in allowed}
    env.update({'GOENV': 'off', 'GOWORK': 'off', 'GOTOOLCHAIN': 'go1.27.3', 'VAULT_CI_BUILD_NETWORK': build_network()})
    return env


def run_process(args, *, timeout=900, check=False, **kwargs):
    # Stop descendants as well as the CLI before deleting their test fixtures.
    with subprocess.Popen(args, start_new_session=True, **kwargs) as process:
        try:
            code = process.wait(timeout=timeout)
        except BaseException:
            try:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=20)
            except ProcessLookupError:
                pass
            except subprocess.TimeoutExpired:
                pass
            # The leader may exit while a descendant ignores SIGTERM.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
            raise
    if check and code:
        raise subprocess.CalledProcessError(code, args)
    return subprocess.CompletedProcess(args, code)


def execute_checks(snapshot, environment, log):
    project = 'vault-ci-' + secrets.token_hex(8)
    environment['CI_IMAGE_TAG'] = project
    compose = ['docker', 'compose', '--project-name', project, '--env-file', '/dev/null', '-f', str(snapshot / 'deployments/docker-compose.ci.yml')]
    def run(args, capture=False):
        if capture:
            return subprocess.check_output(args, cwd=snapshot, env=environment, text=True, timeout=30).strip()
        return run_process(args, cwd=snapshot, env=environment, stdout=log, stderr=subprocess.STDOUT, check=True)
    # Refuse an existing namespace before acquiring cleanup ownership.
    for kind in ('container', 'volume', 'network'):
        if run(['docker', kind, 'ls', '-q', '--filter', 'label=com.docker.compose.project=' + project], True):
            raise ValueError('CI Docker namespace already exists')
    try:
        run(compose + ['build', 'minio'])
        run(compose + ['up', '-d', '--wait', '--wait-timeout', '180', 'postgres', 'minio'])
        run(compose + ['run', '--rm', '--no-deps', 's3-init'])
        run(compose + ['images', '--format', 'json'])
        port = run(compose + ['port', 'postgres', '5432'], True).rsplit(':', 1)[1]
        for variable, database in [('DATABASE', 'auth_test'), ('CHAT_DATABASE', 'chat_test'), ('MEDIA_DATABASE', 'media_test')]:
            run(compose + ['exec', '-T', 'postgres', 'createdb', '-U', 'auth', database])
            environment['VAULT_CHAT_TEST_' + variable + '_URL'] = f'postgres://auth:auth@127.0.0.1:{port}/{database}?sslmode=disable'
        environment['VAULT_CHAT_TEST_MEDIA_S3_ENDPOINT'] = run(compose + ['port', 'minio', '9000'], True)
        environment['VAULT_CHAT_TEST_SCOPED_S3'] = '1'
        return run_process(['xmake', 'ci-checks'], cwd=snapshot, env=environment, stdout=log, stderr=subprocess.STDOUT, timeout=1800).returncode
    finally:
        run(compose + ['down', '--volumes', '--remove-orphans', '--timeout', '20'])


def verify(root):
    folder = receipt_path(root, git(root, 'rev-parse', 'HEAD')).parent
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (folder / 'run.lock').open('w') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise ValueError('verification already running in this repository') from error
        return verify_locked(root)


def verify_locked(root):
    clean(root)
    sha = git(root, 'rev-parse', 'HEAD')
    destination = receipt_path(root, sha)
    destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    destination.unlink(missing_ok=True)  # Failed reruns must not retain old green evidence.
    tools = inventory(root)
    run_dir = Path(tempfile.mkdtemp(prefix='vault-ci-'))  # 0700; never copy ignored local files.
    snapshot = run_dir / 'source'
    log_path = run_dir / 'ci.log'
    print(f'Checking {sha}; private log: {log_path}', flush=True)
    git(root, 'worktree', 'add', '--quiet', '--detach', str(snapshot), sha)
    try:
        with log_path.open('w') as log:
            if execute_checks(snapshot, check_environment(), log) != 0:
                raise ValueError(f'checks failed; inspect {log_path}')
        clean(snapshot)
        if git(snapshot, 'rev-parse', 'HEAD') != sha:
            raise ValueError('snapshot HEAD changed during verification')
        clean(root)
        if git(root, 'rev-parse', 'HEAD') != sha:
            raise ValueError('source HEAD changed during verification')
        if inventory(root) != tools:
            raise ValueError('verification tools changed during checks')
        receipt = {'commit': sha, 'tree': git(root, 'rev-parse', sha + '^{tree}'), 'tools': tools,
                   'completed_at': time.time(), 'log': str(log_path),
                   'excluded': ['live gateway/browser and compose-tag scenarios', 'full remote backup/restore drill', 'production configuration'],
                   'log_sha256': hashlib.sha256(log_path.read_bytes()).hexdigest()}
        temporary = destination.with_suffix('.tmp')
        with open(temporary, 'w', opener=lambda name, flags: os.open(name, flags, 0o600)) as file:
            json.dump(receipt, file, indent=2)
        temporary.replace(destination)
        print(f'Verified {sha}; evidence expires in 24 hours.', flush=True)
        return receipt
    finally:
        # Preserve modified snapshots for inspection; never force-remove them.
        subprocess.run(['git', '-C', str(root), 'worktree', 'remove', str(snapshot)], check=False, stdout=subprocess.DEVNULL)


def merge_policy(pr, sha):
    if pr.get('state') != 'OPEN' or pr.get('isDraft') or pr.get('headRefOid') != sha or pr.get('reviewDecision') != 'APPROVED':
        raise ValueError('merge requires the verified PR head and independent approval of the latest push')


def verify_push(root, lines, tools):
    for line in lines:
        _, pushed, _, _ = line.split()
        if pushed != '0' * 40:
            require_receipt(root, git(root, 'rev-parse', pushed + '^{commit}'), tools)


def main():
    root = Path(output(['git', 'rev-parse', '--show-toplevel']))
    os.environ['PATH'] = str(root / '.tools/bin') + os.pathsep + os.environ.get('PATH', '')
    command = sys.argv[1] if len(sys.argv) > 1 else 'check'
    if command == 'run':
        verify(root)
        return
    clean(root)
    sha = git(root, 'rev-parse', 'HEAD')
    tools = inventory(root)
    if command == 'pre-push':
        verify_push(root, sys.stdin, tools)
        return
    require_receipt(root, sha, tools)
    if command == 'check':
        print(f'Current verification: {sha}')
        return
    repo = output(['gh', 'repo', 'view', '--json', 'nameWithOwner', '--jq', '.nameWithOwner'], root)
    if command == 'publish':
        subprocess.run(['gh', 'api', '--method', 'POST', f'repos/{repo}/statuses/{sha}', '-f', 'state=success',
                        '-f', 'context=vault/local-ci', '-f', 'description=Commit-bound local CI; independent review required'], cwd=root, check=True, stdout=subprocess.DEVNULL)
    elif command == 'merge' and len(sys.argv) == 3 and sys.argv[2].isdigit():
        number = sys.argv[2]
        pr = json.loads(output(['gh', 'pr', 'view', number, '--json', 'state,isDraft,reviewDecision,headRefOid'], root))
        merge_policy(pr, sha)
        # Normal server-enforced merge; no admin or ruleset bypass fallback.
        subprocess.run(['gh', 'api', '--method', 'PUT', f'repos/{repo}/pulls/{number}/merge', '-f', 'sha=' + sha, '-f', 'merge_method=squash'], cwd=root, check=True)
    else:
        raise ValueError('usage: verification.py run|check|pre-push|publish|merge <PR>')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, subprocess.SubprocessError, OSError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
