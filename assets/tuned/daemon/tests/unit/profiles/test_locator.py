import unittest
import os
import shutil
import tempfile
from tuned.profiles.locator import Locator

class LocatorTestCase(unittest.TestCase):
	def setUp(self):
		self.locator = Locator(self._tmp_load_dirs)

	@classmethod
	def setUpClass(cls):
		tmpdir1 = tempfile.mkdtemp()
		tmpdir2 = tempfile.mkdtemp()
		cls._tmp_load_dirs = [tmpdir1, tmpdir2]

		cls._create_profile(tmpdir1, "balanced")
		cls._create_profile(tmpdir1, "powersafe")
		cls._create_profile(tmpdir2, "custom")
		cls._create_profile(tmpdir2, "balanced")

	@classmethod
	def tearDownClass(cls):
		for tmp_dir in cls._tmp_load_dirs:
			shutil.rmtree(tmp_dir, True)

	@classmethod
	def _create_profile(cls, load_dir, profile_name):
		profile_dir = os.path.join(load_dir, profile_name)
		conf_name = os.path.join(profile_dir, "tuned.conf")
		os.mkdir(profile_dir)
		with open(conf_name, "w") as conf_file:
			if profile_name != "custom":
				conf_file.write("[main]\nsummary=this is " + profile_name + "\n")
			else:
				conf_file.write("summary=this is " + profile_name + "\n")

	def test_init(self):
		Locator([])

	def test_init_invalid_type(self):
		with self.assertRaises(TypeError):
			Locator("string")

	def test_get_known_names(self):
		known = self.locator.get_known_names()
		self.assertListEqual(known, ["balanced", "custom", "powersafe"])

	def test_get_config(self):
		config_name = self.locator.get_config("custom")
		self.assertEqual(config_name, os.path.join(self._tmp_load_dirs[1], "custom", "tuned.conf"))
		# none matched, none skipped
		config_name = self.locator.get_config("non-existent")
		self.assertIsNone(config_name)

	def test_get_config_priority(self):
		customized = self.locator.get_config("balanced")
		self.assertEqual(customized, os.path.join(self._tmp_load_dirs[1], "balanced", "tuned.conf"))
		system = self.locator.get_config("balanced", [customized])
		self.assertEqual(system, os.path.join(self._tmp_load_dirs[0], "balanced", "tuned.conf"))
		# none matched, but at least one skipped
		empty = self.locator.get_config("balanced", [customized, system])
		self.assertEqual(empty, "")

	def test_ignore_nonexistent_dirs(self):
		locator = Locator([self._tmp_load_dirs[0], "/tmp/some-dir-which-does-not-exist-for-sure"])
		balanced = locator.get_config("balanced")
		self.assertEqual(balanced, os.path.join(self._tmp_load_dirs[0], "balanced", "tuned.conf"))
		known = locator.get_known_names()
		self.assertListEqual(known, ["balanced", "powersafe"])

	def test_get_known_names_summary(self):
		self.assertEqual(("balanced", "this is balanced"), sorted(self.locator.get_known_names_summary())[0])

	def test_get_profile_attrs(self):
		attrs = self.locator.get_profile_attrs("balanced", ["summary", "wrong_attr"], ["this is default", "this is wrong attr"])
		self.assertEqual([True, "balanced", "this is balanced", "this is wrong attr"],  attrs)

		attrs = self.locator.get_profile_attrs("custom", ["summary"], ["wrongly writen profile"])
		self.assertEqual([True, "custom", "wrongly writen profile"], attrs)

		attrs = self.locator.get_profile_attrs("different", ["summary"], ["non existing profile"])
		self.assertEqual([False, "", "", ""], attrs)
