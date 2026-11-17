import unittest

import server


class Language(unittest.TestCase):
    def test_language(self):
        for text, want in [
            ("Hei!", "no"),
            ("Været blir fint i morgen.", "no"),
            ("Jeg kan hjelpe deg med det.", "no"),
            ("The weather will be fine tomorrow.", "en"),
            ("What can I do for you?", "en"),
            ("OK", "en"),
        ]:
            self.assertEqual(server.language(text), want, text)

    def test_pairs(self):
        self.assertEqual(server.pairs("no=a, en=b,bad,=x"), {"no": "a", "en": "b"})


class Transcribe(unittest.TestCase):
    def test_not_running(self):
        old = server.WHISPER_ADDR
        server.WHISPER_ADDR = ("127.0.0.1", 9)  # nothing listens there
        try:
            code, out = server.transcribe(b"x", "multipart/form-data; boundary=x")
        finally:
            server.WHISPER_ADDR = old
        self.assertEqual(code, 503)
        self.assertIn("not ready", out["error"])

    def test_command(self):
        cmd = server.whisper_cmd()
        self.assertEqual(cmd[0], "whisper-server")
        self.assertIn("127.0.0.1", cmd)  # never on the network: it has no sign-in


if __name__ == "__main__":
    unittest.main()
