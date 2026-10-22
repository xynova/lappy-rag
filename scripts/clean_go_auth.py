# /// script
# requires-python = ">=3.11"
# ///
"""Remove ~/.netrc created for Go proxy auth."""

import pathlib

netrc = pathlib.Path.home() / ".netrc"
if netrc.exists():
    netrc.unlink()
    print(f"Removed Go auth file: {netrc}")
else:
    print("No .netrc file to remove.")
