# sqlite-to-r2

This tool can be used to backup a sqlite db file to R2.

## K8S

To use this tool in K8S, add a side container with this tool.

It expects a few env variabled to be set in order to run:

1. `BUCKET_NAME` - the name of the bucket where the db will be uploaded
2. `ACCOUNT_ID` - CF account id. Check the instruction on how to find it [here](https://developers.cloudflare.com/fundamentals/setup/find-account-and-zone-ids/)
3. `ACCESS_KEY_ID`,and `ACCESS_KEY_SECRET` - the pair used for auth. Check the instructions on how to generate correct keys [here](https://developers.cloudflare.com/r2/api/s3/tokens/)
4. `DB_FILE_PATH` - the path to the sqlite db file for which the backup will be created

Check an example [here](https://github.com/calendar-team/calendar-backend/blob/master/k8s/deployment.yaml)
