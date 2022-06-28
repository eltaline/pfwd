#!/bin/bash

install --mode=755 --owner=root --group=root --directory /var/log/pfwd
systemctl daemon-reload

#END