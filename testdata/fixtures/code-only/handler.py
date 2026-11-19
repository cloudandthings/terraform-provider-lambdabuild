from app.handlers.greet import greet


def lambda_handler(event, context):
    return {"statusCode": 200, "body": greet(event.get("name", "world"))}
