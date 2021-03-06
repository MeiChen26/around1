package main

import  (  
"fmt"

"net/http"  
"encoding/json"  
"log"
"strconv"
"reflect"
"github.com/olivere/elastic"
"github.com/pborman/uuid"
"cloud.google.com/go/storage"
"golang.org/x/net/context"
"io"
)
const  (
    DISTANCE =  "200km"
    POST_INDEX = "post"
    POST_TYPE = "post"
    ES_URL = "http://104.197.27.4:9200"
    BUCKET_NAME = "mt-post-images"
)

type   Location   struct  {
    Lat float64 `json: "lat" `
    Lon float64 `json: "lon" `
}

type  Post struct {
    User   string  `json: "user" ` 
    Message  string  `json: "message" `  
    Location   Location  `json: "location" `
    Url string `json:"url"`
}

func main() {
        fmt.Println( "started service" )
        createIndexIfNotExist()
        http.HandleFunc( "/post" , handlerPost) 
        http.HandleFunc( "/search" , handlerSearch)
        log.Fatal(http.ListenAndServe( ":8080" ,  nil ))
}

func createIndexIfNotExist() {
   client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
   if err != nil {
       panic(err) 
    }
   exists, err := client.IndexExists(POST_INDEX).Do(context.Background()) 
   if err != nil {
   panic(err) 
}
   if !exists { 
       mapping := `{
        "mappings": { 
            "post": {
              "properties": { 
                "location": {
                  "type": "geo_point" 
                }
              } 
            }
        } 
    }`
   _, err = client.CreateIndex(POST_INDEX).Body(mapping).Do(context.Background()) 
   if err != nil {
   panic(err) 
       }
   } 
}

// Save a post to ElasticSearch
func saveToES(post *Post, id string) error {
client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false)) 
if err != nil {
return err 
}
_, err = client.Index().
Index(POST_INDEX).
Type(POST_TYPE).
Id(id).
BodyJson(post). 
Refresh("wait_for"). 
Do(context.Background())
if err != nil { 
    return err
}
fmt.Printf("Post is saved to index: %s\n", post.Message)
return nil 
}

func   handlerPost (w http.ResponseWriter, r *http.Request) {  // Parse from body of request to get a json object. 
    fmt.Println( "Received one post request")

    w.Header().Set("Content-Type", "application/json") 
    w.Header().Set("Access-Control-Allow-Origin", "*") 
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")

    lat, _ := strconv.ParseFloat(r.FormValue("lat"), 64) 
    lon, _ := strconv.ParseFloat(r.FormValue("lon"), 64)
    
    p := &Post{
        User: r.FormValue("user"), 
        Message: r.FormValue("message"), 
        Location: Location{
        Lat: lat,
        Lon: lon, 
      },
    }

    id := uuid.New()

    file, _, err := r.FormFile("image")
    if err != nil {
        http.Error(w, "Image is not available", http.StatusBadRequest) 
        fmt.Printf("Image is not available %v.\n", err)
        return
        }
    attrs, err := saveToGCS(file, BUCKET_NAME, id) 
    if err != nil {
            http.Error(w, "Failed to save image to GCS", http.StatusInternalServerError) 
            fmt.Printf("Failed to save image to GCS %v.\n", err)
            return
    }
    p.Url = attrs.MediaLink
    err = saveToES(p, id) 
    if err != nil {
    http.Error(w, "Failed to save post to ElasticSearch", http.StatusInternalServerError) 
    fmt.Printf("Failed to save post to ElasticSearch %v.\n", err)
    return
    }
    fmt.Println("Saved one post to ElasticSearch: %s", p.Message)
}

func handlerSearch(w http.ResponseWriter, r *http.Request) {  
    fmt.Println("Received one request for search")
    w.Header().Set("Content-Type", "application/json") 
w.Header().Set("Access-Control-Allow-Origin", "*") 
w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")

lat, _ := strconv.ParseFloat(r.URL.Query().Get("lat"), 64) 
lon, _ := strconv.ParseFloat(r.URL.Query().Get("lon"), 64) 
// range is optional
ran := DISTANCE
if val := r.URL.Query().Get("range"); val != "" { 
    ran = val + "km"
}
posts, err := readFromES(lat, lon, ran) 
if err != nil {
http.Error(w, "Failed to read post from ElasticSearch", http.StatusInternalServerError) 
fmt.Printf("Failed to read post from ElasticSearch %v.\n", err)
return
}

js, err := json.Marshal(posts) 
if err != nil {
http.Error(w, "Failed to parse posts into JSON format", http.StatusInternalServerError) 
fmt.Printf("Failed to parse posts into JSON format %v.\n", err)
return
}
w.Write(js) 
}

func readFromES(lat, lon float64, ran string) ([]Post, error) {
    client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false)) 
    if err != nil {
    return nil, err 
   }

    query := elastic.NewGeoDistanceQuery("location") 
    query = query.Distance(ran).Lat(lat).Lon(lon)
    searchResult, err := client.Search().Index(POST_INDEX).Query(query).Pretty(true).Do(context.Background())
    if err != nil { 
        return nil, err
    }
    fmt.Printf("Query took %d milliseconds\n", searchResult.TookInMillis)
    
    var ptyp Post
    var posts []Post
    for _, item := range searchResult.Each(reflect.TypeOf(ptyp)) {
        if p, ok := item.(Post); ok { 
        posts = append(posts, p)
       } 
   }
    return posts, nil 
}

func saveToGCS(r io.Reader, bucketName, objectName string) (*storage.ObjectAttrs, error) { 
    ctx := context.Background()
    // Creates a client.
    client, err := storage.NewClient(ctx, option.WithCredentialsFile("Unknown-3"))
    if err != nil {
    return nil, err 
}
    bucket := client.Bucket(bucketName) 
    if _, err := bucket.Attrs(ctx); err != nil {
    return nil, err 
}
    object := bucket.Object(objectName) 
    wc := object.NewWriter(ctx)
if _, err = io.Copy(wc, r); err != nil {
return nil, err 
}
if err := wc.Close(); err != nil { 
    return nil, err
}
if err = object.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil { 
    return nil, err
}
attrs, err := object.Attrs(ctx) 
if err != nil {
return nil, err 
}
fmt.Printf("Image is saved to GCS: %s\n", attrs.MediaLink)
return attrs, nil 
}