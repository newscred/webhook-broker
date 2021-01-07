#!/usr/bin/python

import os
import json
import sys


def main():
    vpn_id = sys.argv[1]
    stream = os.popen(f"aws ec2 export-client-vpn-client-configuration --client-vpn-endpoint-id {vpn_id} --output text")
    output = stream.read()
    print(json.dumps({"output": output}))


if __name__ == "__main__":
    main()
