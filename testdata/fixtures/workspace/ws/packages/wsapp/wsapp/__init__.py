from wslib import greet


def handler(event, context):
    return greet("app")
