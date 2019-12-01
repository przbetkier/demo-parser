### Faceit CS:GO Demo parser for AWS Lambda

Development:

Run command:
```
 GOOS=linux go build -o main
```

It will create 'main' linux executable.
Compress it to a zip file and deploy on your AWS lambda env.

Set environment variable `API_ENDPOINT` defining where to POST parsed data.

Parsed data format:

```
{
  "matchId": "matchId-1",
  "data": [
    {
      "nickname": "player-1",
      "plants": 1,
      "defusals": 1,
      "flashed": 1,
      "kills": [
        {
          "victim": "player-2",
          "kPos": {
            "X": 686.6844144780584,
            "Y": 586.2584150598404
          },
          "vPos": {
            "X": 617.2221926425366,
            "Y": 444.6832470183677
          },
          "wb": true,
          "hs": false,
          "entry": false,
          "weapon": "FAMAS"
        },
        ...
      ],
      "deaths": [
        {
          "killer": "player-2",
          "kPos": {
            "X": 686.6844144780584,
            "Y": 586.2584150598404
          },
          "vPos": {
            "X": 617.2221926425366,
            "Y": 444.6832470183677
          },
          "wb": true,
          "hs": false,
          "entry": false,
          "weapon": "FAMAS"
        },
        ...
      ]
    }
  ]
}
```

Function can be invoked e.g. by executing POST request on API Gateway:

```
{
  "matchId": "your-match-id",
  "demoUrl": "https://demos-europe-west2.faceit-cdn.net/csgo/5xxxxxx-1111-yyyy-xxxx-bbe47ce6f10b.dem.gz"
}
``` 
