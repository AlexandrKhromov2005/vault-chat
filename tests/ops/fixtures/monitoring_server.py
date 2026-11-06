import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ssl
import threading


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'monitoring_acceptance_up 1\n' if self.path == '/metrics' else b'ok\n'
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format, *_args):
        pass


def server(address, port, tls=False):
    result = ThreadingHTTPServer((address, port), Handler)
    if tls:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain('/fixture/tls.crt', '/fixture/tls.key')
        result.socket = context.wrap_socket(result.socket, server_side=True)
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('address')
    parser.add_argument('port', type=int)
    parser.add_argument('--https-port', type=int)
    args = parser.parse_args()
    if args.https_port:
        https = server(args.address, args.https_port, tls=True)
        threading.Thread(target=https.serve_forever, daemon=True).start()
    server(args.address, args.port).serve_forever()


if __name__ == '__main__':
    main()
