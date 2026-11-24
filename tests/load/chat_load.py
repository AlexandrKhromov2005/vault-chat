#!/usr/bin/env python3
"""Disposable Chat/Web load acceptance on the VPS configuration.

Builds the images of the checkout, starts deployments/docker-compose.vps.yml
with tests/load/stack.yml and synthetic secrets in a private temporary
directory, runs the generator (cmd/chat-load) in the stand's network and
answers its phase marks with snapshots of the containers, PostgreSQL and
Redis. Writes the machine-readable report and exits with the generator's
code: 0 all gates passed, 3 a gate failed, 1 the run failed.
See docs/load/chat.md.
"""
import argparse
import contextlib
import datetime
import hashlib
import http.client
import importlib.util
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('rpc_certs', ROOT / 'scripts/gen-rpc-certs.py')
certs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(certs)

# The VPS has 2 vCPU (docs/deployment.md); the generator gets four others.
STAND_CPUS = '0,1'
CLIENT_CPUS = '2-5'
REQUIRED_CPUS = 6
IMAGES = ('auth', 'chat', 'media', 'gateway', 'minio', 'chat-load')
DATABASES = {'auth': ('postgres', 'auth', 'auth_db'), 'chat': ('chat-postgres', 'chat', 'chat_db')}
PG_COLUMNS = ('xact_commit', 'xact_rollback', 'tup_inserted', 'tup_fetched', 'blks_read', 'blks_hit',
              'deadlocks', 'numbackends', 'temp_bytes')
REDIS_FIELDS = ('total_commands_processed', 'total_net_input_bytes', 'total_net_output_bytes', 'used_memory',
                'connected_clients', 'pubsub_channels', 'rejected_connections')


class UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('localhost', timeout=30)
        self.socket_path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(30)
        self.sock.connect(self.socket_path)


class Docker:
    """Read-only client of the local Engine API."""

    def __init__(self, endpoint):
        self.path = urllib.parse.urlparse(endpoint).path

    def get(self, path):
        connection = UnixHTTPConnection(self.path)
        try:
            connection.request('GET', path)
            response = connection.getresponse()
            body = response.read()
            if response.status != 200:
                raise RuntimeError(f'Docker API {path}: {response.status} {body[:200]!r}')
            return json.loads(body)
        finally:
            connection.close()

    def containers(self, project):
        filters = urllib.parse.quote(json.dumps({'label': [f'com.docker.compose.project={project}'],
                                                 'status': ['running']}))
        return {c['Labels']['com.docker.compose.service']: c['Id']
                for c in self.get(f'/containers/json?filters={filters}')}


def memory(stats):
    """Memory as `docker stats` shows it: usage without the inactive page cache."""
    m = stats.get('memory_stats') or {}
    usage = m.get('usage', 0)
    detail = m.get('stats') or {}
    return max(0, usage - detail.get('inactive_file', detail.get('total_inactive_file', 0)))


class Sampler(threading.Thread):
    """Samples container memory every second for the peak between marks."""

    def __init__(self, docker, project):
        super().__init__(daemon=True)
        self.docker, self.project = docker, project
        self.lock = threading.Lock()
        self.peaks = {}
        self.stopped = threading.Event()

    def run(self):
        while not self.stopped.wait(1):
            try:
                for service, cid in self.docker.containers(self.project).items():
                    value = memory(self.docker.get(f'/containers/{cid}/stats?stream=false&one-shot=true'))
                    with self.lock:
                        self.peaks[service] = max(self.peaks.get(service, 0), value)
            except (OSError, RuntimeError, ValueError):
                continue

    def take_peaks(self):
        with self.lock:
            peaks, self.peaks = self.peaks, {}
        return peaks


def port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return str(listener.getsockname()[1])


def openssl(*args):
    subprocess.run(['openssl', *map(str, args)], check=True, capture_output=True)


def gateway_tls(directory):
    """A synthetic CA and a Gateway certificate for gateway and 127.0.0.1."""
    directory.mkdir(mode=0o755)
    ca_key, ca = directory / 'ca.key', directory / 'ca.pem'
    openssl('req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-days', '2',
            '-subj', '/CN=Vault Chat load CA', '-addext', 'basicConstraints=critical,CA:TRUE,pathlen:0',
            '-addext', 'keyUsage=critical,keyCertSign', '-keyout', ca_key, '-out', ca)
    extensions = directory / 'leaf.cnf'
    extensions.write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\n'
                          'extendedKeyUsage=serverAuth\nsubjectAltName=DNS:gateway,IP:127.0.0.1\n')
    request = directory / 'leaf.csr'
    openssl('req', '-new', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-subj', '/CN=gateway',
            '-keyout', directory / 'privkey.pem', '-out', request)
    openssl('x509', '-req', '-in', request, '-CA', ca, '-CAkey', ca_key, '-set_serial',
            str(int.from_bytes(os.urandom(16), 'big') >> 1 or 1), '-days', '2', '-extfile', extensions,
            '-out', directory / 'fullchain.pem')
    for name in ('ca.key', 'leaf.cnf', 'leaf.csr'):
        (directory / name).unlink()
    # The containers run as another user; the parent directory is private.
    for name in ('ca.pem', 'fullchain.pem', 'privkey.pem'):
        (directory / name).chmod(0o644)


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()


def stand(docker, compose_version):
    info = docker.get('/info')
    return {
        'commit': git('rev-parse', 'HEAD'),
        'dirty': bool(git('status', '--porcelain')),
        'docker': info.get('ServerVersion'),
        'compose': compose_version,
        'os': info.get('OperatingSystem'),
        'kernel': info.get('KernelVersion'),
        'host_cpus': info.get('NCPU'),
        'host_memory_bytes': info.get('MemTotal'),
        'cgroup': info.get('CgroupVersion'),
        'stand_cpuset': STAND_CPUS,
        'generator_cpuset': CLIENT_CPUS,
        'configuration': 'deployments/docker-compose.vps.yml + tests/load/stack.yml',
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--report', type=Path, help='report path (default build/load/chat-load-<UTC time>.json)')
    args = parser.parse_args()
    endpoint = subprocess.check_output(
        ['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], text=True).strip()
    if not endpoint.startswith('unix://'):
        raise RuntimeError('load acceptance requires a local Unix Docker endpoint')
    docker = Docker(endpoint)
    if docker.get('/info').get('NCPU', 0) < REQUIRED_CPUS:
        raise RuntimeError(f'load acceptance needs {REQUIRED_CPUS} CPUs: {STAND_CPUS} for the stand, '
                           f'{CLIENT_CPUS} for the generator')
    started = datetime.datetime.now(datetime.timezone.utc)
    report_path = args.report or ROOT / 'build/load' / f"chat-load-{started.strftime('%Y%m%dT%H%M%SZ')}.json"
    compose_version = subprocess.check_output(['docker', 'compose', 'version', '--short'], text=True).strip()
    project = f'vault-chat-load-{os.getpid()}-{time.time_ns()}'
    tag = f'load-{os.getpid()}-{time.time_ns()}'
    with tempfile.TemporaryDirectory(prefix='vault-chat-load-') as directory:
        temporary = Path(directory)
        temporary.chmod(0o700)
        subprocess.run([str(ROOT / 'scripts/gen-jwt-keys.sh'), str(temporary / 'jwt')], check=True, capture_output=True)
        with contextlib.redirect_stdout(sys.stderr):
            certs.issue(temporary)
        gateway_tls(temporary / 'tls')
        env = dict(
            os.environ,
            COMPOSE_PROJECT_NAME=project, VAULT_IMAGE_TAG=tag, PUBLIC_IP='127.0.0.1',
            LOAD_DIR=str(temporary), LOAD_STAND_CPUS=STAND_CPUS, LOAD_CLIENT_CPUS=CLIENT_CPUS,
            LOAD_GATEWAY_PORT=port(),
        )
        for name in ('AUTH_DB_PASSWORD', 'CHAT_DB_PASSWORD', 'MEDIA_DB_PASSWORD', 'AUTH_RUNTIME_DB_PASSWORD',
                     'CHAT_RUNTIME_DB_PASSWORD', 'MEDIA_RUNTIME_DB_PASSWORD', 'REDIS_PASSWORD',
                     'PROFILE_CACHE_PASSWORD', 'S3_ACCESS_KEY', 'S3_SECRET_KEY', 'S3_ROOT_ACCESS_KEY',
                     'S3_ROOT_SECRET_KEY'):
            env[name] = secrets.token_hex(16)
        compose = ['docker', 'compose', '-p', project, '-f', str(ROOT / 'deployments/docker-compose.vps.yml'),
                   '-f', str(ROOT / 'tests/load/stack.yml')]

        def run(command, check=True, timeout=120, **kwargs):
            result = subprocess.run(command, cwd=ROOT, env=env, text=True, capture_output=True, timeout=timeout, **kwargs)
            if check and result.returncode:
                raise RuntimeError(f"command failed ({result.returncode}): {' '.join(command[:8])}\n"
                                   f'{result.stdout[-4000:]}\n{result.stderr[-4000:]}')
            return result.stdout

        def postgres():
            result = {}
            for name, (service, user, database) in DATABASES.items():
                row = run(compose + ['exec', '-T', service, 'psql', '-U', user, '-d', database, '-AtF,', '-c',
                                     f"SELECT {','.join(PG_COLUMNS)} FROM pg_stat_database WHERE datname = current_database()"])
                result[name] = dict(zip(PG_COLUMNS, map(int, row.strip().split(','))))
            return result

        def redis():
            info = run(compose + ['exec', '-T', 'redis', 'sh', '-c',
                                  'exec redis-cli --no-auth-warning -a "$REDIS_PASSWORD" INFO'])
            values = dict(line.split(':', 1) for line in info.splitlines() if ':' in line and not line.startswith('#'))
            return {'redis': {field: int(values.get(field, '0').strip()) for field in REDIS_FIELDS}}

        def snapshot(sampler):
            containers = {}
            peaks = sampler.take_peaks()
            for service, cid in docker.containers(project).items():
                stats = docker.get(f'/containers/{cid}/stats?stream=false&one-shot=true')
                state = docker.get(f'/containers/{cid}/json')
                current = memory(stats)
                containers[service] = {
                    'cpu_ns': stats['cpu_stats']['cpu_usage']['total_usage'],
                    'memory': current,
                    'memory_peak': max(current, peaks.get(service, 0)),
                    'memory_limit': stats.get('memory_stats', {}).get('limit', 0),
                    'restarts': state.get('RestartCount', 0),
                    'oom_killed': state['State'].get('OOMKilled', False),
                }
            return {'time': time.time(), 'containers': containers, 'postgres': postgres(), 'redis': redis()}

        sampler = Sampler(docker, project)
        report = None
        code = 1
        try:
            print(f'building images {tag} ...', file=sys.stderr, flush=True)
            run(compose + ['--profile', 'load', 'build'], timeout=3600)
            print('starting the stand ...', file=sys.stderr, flush=True)
            run(compose + ['up', '-d', '--wait', '--no-build'], timeout=900)
            profile = json.loads((ROOT / 'internal/chatload/profile.json').read_text())
            invitations = []
            values = []
            for i in range(profile['users']):
                email, token = f'load{i:04d}@example.com', secrets.token_hex(32)
                invitations.append({'email': email, 'invitation_token': token})
                values.append(f"('\\x{hashlib.sha256(token.encode()).hexdigest()}'::bytea, '{email}', now() + interval '1 day')")
            run(compose + ['exec', '-T', 'postgres', 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'auth', '-d', 'auth_db'],
                input='INSERT INTO registration_invitations (token_hash, email, expires_at) VALUES\n'
                      + ',\n'.join(values) + ';\n')
            sampler.start()
            generator = subprocess.Popen(compose + ['--profile', 'load', 'run', '--rm', '-T', '--no-deps', 'loadgen'],
                                         cwd=ROOT, env=env, text=True, stdin=subprocess.PIPE, stdout=subprocess.PIPE)
            generator.stdin.write(json.dumps({'invitations': invitations,
                                              'stand': stand(docker, compose_version)}) + '\n')
            generator.stdin.flush()
            for line in generator.stdout:
                event = json.loads(line)
                if event.get('event') == 'mark':
                    try:
                        reply = {'snapshot': snapshot(sampler)}
                    except (OSError, RuntimeError, ValueError, KeyError, subprocess.SubprocessError) as error:
                        reply = {'error': str(error)}
                    generator.stdin.write(json.dumps(reply) + '\n')
                    generator.stdin.flush()
                elif event.get('event') == 'report':
                    report = event
            generator_code = generator.wait()
            if report is None:
                raise RuntimeError(f'generator exited with {generator_code} without a report')
            report_path.parent.mkdir(parents=True, exist_ok=True)
            report_path.write_text(json.dumps(report['report'], indent=2) + '\n')
            summarize(report['report'], report_path)
            # The verdict comes from the report: `compose run` does not
            # reliably return the container's exit code.
            if report.get('error'):
                print(f"run failed: {report['error']}", file=sys.stderr)
                code = 1
            elif not report['report'].get('passed'):
                code = 3
            else:
                code = 0 if generator_code == 0 else 1
        except BaseException:
            print(run(compose + ['ps', '-a'], check=False), file=sys.stderr)
            print(run(compose + ['logs', '--tail', '60', 'auth', 'chat', 'gateway'], check=False), file=sys.stderr)
            raise
        finally:
            sampler.stopped.set()
            run(compose + ['--profile', 'load', 'down', '-v', '--remove-orphans'], check=False, timeout=300)
            run(['docker', 'image', 'rm', *[f'vault-chat/{image}:{tag}' for image in IMAGES]], check=False)
        return code


def summarize(report, path):
    rows = [report.get('warmup') or {}, *(report.get('series') or []), report.get('reconnect') or {},
            *(report.get('ramp') or [])]
    print(f"{'phase':<12} {'rate':>5} {'msg/s':>6} {'send p95/p99':>13} {'deliv p95/p99':>14} {'read p95':>8} "
          f"{'errors':>6} {'integrity':>9} {'stand CPU':>9} busiest", file=sys.stderr)
    for p in rows:
        if not p.get('name'):
            continue
        resources = p.get('resources') or {}
        integrity = p.get('integrity') or {}
        violations = sum(v for k, v in integrity.items() if k not in ('rooms', 'messages'))
        print(f"{p['name']:<12} {p['target_send_rate']:>5g} {p['throughput']:>6.1f} "
              f"{p['send']['p95_ms']:>6.1f}/{p['send']['p99_ms']:<6.1f} {p['delivery']['p95_ms']:>6.1f}/{p['delivery']['p99_ms']:<7.1f} "
              f"{p['read']['p95_ms']:>8.1f} {sum((p.get('errors') or {}).values()):>6} {violations:>9} "
              f"{resources.get('stand_cpu_cores', 0):>9.2f} {resources.get('busiest', '-')}", file=sys.stderr)
    capacity = report.get('capacity') or {}
    print(f"capacity: passing up to {capacity.get('highest_passing_rate')} msg/s, first failing "
          f"{capacity.get('first_failing_rate')} {capacity.get('failing_gates') or ''}", file=sys.stderr)
    for violation in report.get('violations') or []:
        print(f"gate failed: {violation['phase']} {violation['gate']} = {violation['value']:g} "
              f"(limit {violation['limit']:g})", file=sys.stderr)
    print(f"{'PASSED' if report.get('passed') else 'FAILED'}; report: {path}", file=sys.stderr)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        sys.exit(f'chat load acceptance failed: {error}')
