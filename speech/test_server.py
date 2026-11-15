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


if __name__ == "__main__":
    unittest.main()
