import numpy


def handler(event, context):
    return {"sum": int(numpy.arange(4).sum())}
