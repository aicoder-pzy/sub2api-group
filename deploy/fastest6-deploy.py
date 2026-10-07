import pathlib
import subprocess

source = pathlib.Path(__file__).with_name('fastest4-deploy.sh').read_text()
source = source.replace('fastest.4', 'fastest.6').replace('fastest.3', 'fastest.5').replace('20261006', '20261007')
subprocess.run(['bash'], input=source, text=True, check=True)
