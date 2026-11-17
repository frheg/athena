import unittest

import server


def points(**at):
    """33 pose points, all unseen, except those given as name=(x, y)"""
    p = [[0.5, 0.5, 0.0] for _ in range(33)]
    for name, (x, y) in at.items():
        p[getattr(server, name)] = [x, y, 0.9]
    return p


class Poses(unittest.TestCase):
    def test_arm_up(self):
        p = points(NOSE=(0.5, 0.3), L_WR=(0.6, 0.1), L_SH=(0.6, 0.45), R_WR=(0.4, 0.8), R_SH=(0.4, 0.45))
        self.assertEqual(server.poses(p), ["left arm up"])

    def test_both_up(self):
        p = points(NOSE=(0.5, 0.3), L_WR=(0.6, 0.1), L_SH=(0.6, 0.45), R_WR=(0.4, 0.1), R_SH=(0.4, 0.45))
        self.assertEqual(server.poses(p), ["both arms up"])

    def test_nothing_seen(self):
        self.assertEqual(server.poses(points()), [])

    def test_pairs(self):
        self.assertEqual(server.pairs("core=a, bad,=x"), {"core": "a"})


if __name__ == "__main__":
    unittest.main()
