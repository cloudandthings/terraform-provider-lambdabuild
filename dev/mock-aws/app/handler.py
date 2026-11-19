# requests comes from the deps layer (function "this") or is bundled in the
# zip (function "bundled").
import requests


def handler(event, context):
    return {"requests": requests.__version__}
