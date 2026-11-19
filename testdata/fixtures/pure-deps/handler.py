import six
from tabulate import tabulate


def handler(event, context):
    return {"table": tabulate([[six.text_type("ok")]])}
