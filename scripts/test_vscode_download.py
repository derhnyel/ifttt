"""Exercise pinned host downloads against a real local HTTP server."""
import contextlib
import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import importlib.util
import io
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch


class VSCodeDownloadTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.destination = Path(self.tmp.name) / 'vscode.deb'
        self.destination.write_bytes(b'previous verified archive')
        path = Path(__file__).resolve().parents[1] / 'test/install-vscode.py'
        spec = importlib.util.spec_from_file_location('install_vscode', path)
        self.module = importlib.util.module_from_spec(spec)
        with patch('urllib.request.urlopen', return_value=io.BytesIO(b'')):
            spec.loader.exec_module(self.module)

    @contextlib.contextmanager
    def server(self, responses):
        requests = []
        destination = self.destination
        test = self
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                test.assertEqual(destination.read_bytes(), b'previous verified archive')
                requests.append((self.path, self.headers.get('Cache-Control')))
                status, body = responses[min(len(requests)-1, len(responses)-1)]
                if status == 'truncated':
                    self.send_response(200)
                    self.send_header('Transfer-Encoding', 'chunked')
                    self.end_headers()
                    self.wfile.write(b'10\r\nshort')
                    self.close_connection = True
                    return
                self.send_response(status)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            def log_message(self, *_args):
                pass
        server = HTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            yield f'http://127.0.0.1:{server.server_port}/pinned.deb', requests
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

    # LINT.IfChange(verified_host_download)
    def test_corrupt_response_is_retried_and_only_verified_bytes_replace_existing_archive(self):
        body = b'new verified archive'
        with self.server([(200,b'cached wrong bytes'),(200,body)]) as (url, requests), patch('time.sleep'), patch('sys.stderr',new=io.StringIO()):
            self.module.download(url, hashlib.sha256(body).hexdigest(), self.destination)
        self.assertEqual(self.destination.read_bytes(), body)
        self.assertEqual(len(requests), 2)
        self.assertTrue(all(headers == 'no-cache' for _, headers in requests))
        self.assertNotEqual(requests[0][0], requests[1][0])

    def test_persistent_corruption_reports_evidence_and_preserves_previous_archive(self):
        with self.server([(200,b'wrong')]) as (url, requests), patch('time.sleep'), patch('sys.stderr',new=io.StringIO()):
            with self.assertRaisesRegex(ValueError, 'received 5 bytes'):
                self.module.download(url, '0'*64, self.destination)
        self.assertEqual(len(requests), 3)
        self.assertEqual(self.destination.read_bytes(), b'previous verified archive')
        self.assertFalse(self.destination.with_suffix('.tmp').exists())

    def test_http_errors_and_partial_responses_cannot_be_installed(self):
        body = b'new verified archive'
        with self.server([(503,b'unavailable'),(206,body),(200,body)]) as (url, requests), patch('time.sleep'), patch('sys.stderr',new=io.StringIO()):
            self.module.download(url, hashlib.sha256(body).hexdigest(), self.destination)
        self.assertEqual(len(requests), 3)
        self.assertEqual(self.destination.read_bytes(), body)
    def test_truncated_chunked_response_is_retried_before_installation(self):
        body = b'new verified archive'
        with self.server([('truncated',b''),(200,body)]) as (url, requests), patch('time.sleep'), patch('sys.stderr',new=io.StringIO()):
            self.module.download(url, hashlib.sha256(body).hexdigest(), self.destination)
        self.assertEqual(len(requests), 2)
        self.assertEqual(self.destination.read_bytes(), body)

    # LINT.ThenChange(//test/install-vscode.py:verified_host_download)
