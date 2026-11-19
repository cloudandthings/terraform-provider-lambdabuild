import numpy


def handler(event, context):
    return {"sum": int(numpy.arange(5).sum()), "numpy": numpy.__version__}
