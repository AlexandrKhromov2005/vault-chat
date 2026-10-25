"""Small local security gates. Findings contain paths/rules, never secret values."""
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

GO_TOOLS = {'golangci-lint': 'v2.14.0', 'govulncheck': 'v1.9.0'}


def output(args, cwd=None, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env, text=True, stderr=subprocess.STDOUT, timeout=30).strip()


def docker_endpoint(environment=None):
    # Ask this Docker CLI to resolve its actual config/context/HOST precedence.
    endpoint = output(['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], env=environment)
    if not endpoint.startswith('unix:///'):
        raise ValueError('verified CI requires an absolute local Unix Docker endpoint; remote daemons are forbidden')
    return endpoint


def version(value):
    match = re.search(r'(\d+)\.(\d+)(?:\.(\d+))?', value)
    if not match:
        raise ValueError('cannot parse tool version')
    return tuple(int(n or 0) for n in match.groups())


def check_node(value):
    if version(value) != (22, 23, 4):
        raise ValueError('Node.js 22.23.4 required for verified CI (includes the current 22.x security fixes)')


def check_go_tool(name, built, target):
    if version(built) < version(target):
        raise ValueError(f'{name} was built with {built}; rebuild using Go {target}')


def build_network():
    mode = os.environ.get('VAULT_CI_BUILD_NETWORK', 'default')
    if mode not in {'default', 'host'}:
        raise ValueError('CI build network must be default or host; arbitrary Docker networks are forbidden')
    return mode


def secret_findings(name, data):
    parts = Path(name).parts
    findings = []
    if any(p in {'secrets', 'backups', '.env'} or (p.startswith('.env.') and not p.endswith('.example')) for p in parts):
        findings.append('local configuration or secret directory')
    if re.search(rb'-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----\r?\n[A-Za-z0-9+/=\r\n]+-----END (?:[A-Z]+ )?PRIVATE KEY-----', data):
        findings.append('embedded private key')
    if re.search(rb'\bAGE-SECRET-KEY-1[0-9A-Z]{50,}\b', data):
        findings.append('age private identity')
    if re.search(rb'\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{50,}|AKIA[A-Z0-9]{16})\b', data):
        findings.append('known credential format')
    return findings


def scan(root, staged=False):
    args = ['git', '-C', str(root)]
    listing = subprocess.check_output(args + (['ls-files', '--stage', '-z'] if staged else ['ls-tree', '-rz', 'HEAD']))
    errors = []
    for entry in listing.split(b'\0'):
        if not entry:
            continue
        meta, name = entry.split(b'\t', 1)
        fields = meta.split()
        # Index: mode oid stage; tree: mode type oid.
        oid = fields[1] if staged else fields[2]
        if fields[0] == b'160000':
            errors.append(f'{os.fsdecode(name)!r}: submodules require a separate security review')
            continue
        data = subprocess.check_output(args + ['cat-file', 'blob', oid.decode()])
        for rule in secret_findings(os.fsdecode(name), data):
            errors.append(f'{os.fsdecode(name)!r}: {rule}')
    if errors:
        raise ValueError('secret scan failed:\n' + '\n'.join(errors))


def inventory(root):
    if sys.version_info < (3, 11):
        raise ValueError('Python 3.11+ required')
    target = re.search(r'^toolchain go(\S+)$', (root / 'go.mod').read_text(), re.M).group(1)
    go = output(['go', 'version'])
    if version(go) != version(target):
        raise ValueError(f'Go {target} required; found {go}')
    programs = ['go', 'node', 'python3', 'xmake', 'docker', 'buf', 'openssl', 'age', 'age-keygen', 'cc', *GO_TOOLS]
    result = {'go': go, 'python': sys.version, 'profile': 'local-ci-v2', 'build_network': build_network()}
    for program in programs:
        binary = shutil.which(program)
        if not binary:
            raise ValueError(f'{program} required in PATH (see CONTRIBUTING.md)')
        with open(binary, 'rb') as file:
            result[program + '_sha256'] = hashlib.file_digest(file, 'sha256').hexdigest()
    for name, binary in [('selected_go', Path(output(['go', 'env', 'GOROOT'])) / 'bin/go'),
                         ('compiler', Path(output(['go', 'env', 'GOTOOLDIR'])) / 'compile')]:
        with binary.open('rb') as file:
            result[name + '_sha256'] = hashlib.file_digest(file, 'sha256').hexdigest()
    check_node(output(['node', '--version']))
    for name, expected in GO_TOOLS.items():
        build = output(['go', 'version', '-m', shutil.which(name)])
        check_go_tool(name, build.splitlines()[0].rsplit(': ', 1)[-1], target)
        if not re.search(r'\tmod\t\S+\t' + re.escape(expected) + r'(?:\t|$)', build, re.M):
            raise ValueError(f'{name} {expected} required (see CONTRIBUTING.md)')
    if output(['buf', '--version']) != '1.47.2':
        raise ValueError('buf 1.47.2 required')
    result['compose'] = output(['docker', 'compose', 'version', '--short'])
    result['docker_endpoint'] = docker_endpoint()
    output(['docker', 'info', '--format', '{{.ServerVersion}}'])
    for program, flag in [('age', '--version'), ('age-keygen', '--version'), ('openssl', 'version')]:
        result[program] = output([program, flag])
    return result


if __name__ == '__main__':
    try:
        root = Path(output(['git', 'rev-parse', '--show-toplevel']))
        os.environ['PATH'] = str(root / '.tools/bin') + os.pathsep + os.environ.get('PATH', '')
        if sys.argv[1:] == ['doctor']:
            inventory(root)
            print('Security tools ready; Go analyzers pinned and compatible, crypto tools present.')
        else:
            scan(root, '--staged' in sys.argv)
            print('Tracked-secret scan passed (heuristic; review is still required).')
    except (ValueError, subprocess.SubprocessError, OSError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
